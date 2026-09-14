let map;
let ws;
let vehicleMarkers = {};
let congestionLines = {};
let congestionOverlayOn = true;
let hexPolygons = {};
let hexOverlayOn = false;
let surgePolygons = {};
let surgeOverlayOn = false;
let mapRecentered = false;
let paused = false;
let stopped = false;

const STATE_LABELS = { idle: 'idle', repositioning: 'repositioning', enroute: 'on trip', assigned: 'to pickup' };

function initMap() {
    map = L.map('map').setView([37.7749, -122.4194], 13);

    L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
        attribution: '© OpenStreetMap contributors',
        maxZoom: 19
    }).addTo(map);
}

function getVehicleColor(state) {
    switch (state) {
        case 'idle':
            return '#00ff88';
        case 'enroute':
            return '#00ddff';
        case 'assigned':
            return '#ffaa00';
        case 'repositioning':
            return '#b388ff';
        default:
            return '#ffffff';
    }
}

function updateVehicle(vehicle) {
    const node = vehicle.node === undefined ? '' : `<br>Node: ${vehicle.node}`;
    const popup = `Vehicle ${vehicle.id}<br>${STATE_LABELS[vehicle.state] || vehicle.state}${node}`;
    if (vehicleMarkers[vehicle.id]) {
        const m = vehicleMarkers[vehicle.id];
        m.setLatLng([vehicle.lat, vehicle.lon]);
        m.setStyle({ fillColor: getVehicleColor(vehicle.state) });
        m.setPopupContent(popup); // otherwise it keeps the state from creation
    } else {
        const marker = L.circleMarker([vehicle.lat, vehicle.lon], {
            radius: 6,
            fillColor: getVehicleColor(vehicle.state),
            color: '#000',
            weight: 1,
            opacity: 1,
            fillOpacity: 0.8
        }).addTo(map);
        marker.bindPopup(popup);
        vehicleMarkers[vehicle.id] = marker;
    }
}

// The middle 90% of the fleet, so a few far-off cars don't zoom the map out.
function fleetBounds(vehicles) {
    const lats = vehicles.map(v => v.lat).sort((a, b) => a - b);
    const lons = vehicles.map(v => v.lon).sort((a, b) => a - b);
    const at = (xs, p) => xs[Math.min(xs.length - 1, Math.floor(p * xs.length))];
    return L.latLngBounds([at(lats, 0.05), at(lons, 0.05)], [at(lats, 0.95), at(lons, 0.95)]);
}

// Cells with idle drivers, colored by count.
function updateHexes(hexes) {
    const seen = new Set();
    (hexes || []).forEach(h => {
        seen.add(h.cell);
        const fill = hexColor(h.driver_count);
        const label = `${h.driver_count} idle driver(s)`;
        if (hexPolygons[h.cell]) {
            hexPolygons[h.cell].setStyle({ fillColor: fill });
            hexPolygons[h.cell].setLatLngs(h.boundary);
            hexPolygons[h.cell].setTooltipContent(label);
        } else {
            const poly = L.polygon(h.boundary, {
                color: '#222',
                weight: 1,
                fillColor: fill,
                fillOpacity: 0.35,
            }).addTo(map);
            poly.bindTooltip(label);
            hexPolygons[h.cell] = poly;
        }
    });
    Object.keys(hexPolygons).forEach(cell => {
        if (!seen.has(cell)) {
            map.removeLayer(hexPolygons[cell]);
            delete hexPolygons[cell];
        }
    });
}

function clearHexes() {
    Object.values(hexPolygons).forEach(p => map.removeLayer(p));
    hexPolygons = {};
}

function hexColor(count) {
    if (count >= 5) return '#ff4444';
    if (count >= 3) return '#ffaa00';
    return '#00ff88';
}

// Surge cells, colored by multiplier.
function updateSurge(cells) {
    const seen = new Set();
    (cells || []).forEach(c => {
        seen.add(c.cell);
        const fill = surgeColor(c.multiplier);
        const label = `surge ${c.multiplier.toFixed(2)}x`;
        if (surgePolygons[c.cell]) {
            surgePolygons[c.cell].setStyle({ fillColor: fill });
            surgePolygons[c.cell].setLatLngs(c.boundary);
            surgePolygons[c.cell].setTooltipContent(label);
        } else {
            const poly = L.polygon(c.boundary, {
                color: '#900',
                weight: 1,
                fillColor: fill,
                fillOpacity: 0.4,
            }).addTo(map);
            poly.bindTooltip(label);
            surgePolygons[c.cell] = poly;
        }
    });
    Object.keys(surgePolygons).forEach(cell => {
        if (!seen.has(cell)) {
            map.removeLayer(surgePolygons[cell]);
            delete surgePolygons[cell];
        }
    });
}

function clearSurge() {
    Object.values(surgePolygons).forEach(p => map.removeLayer(p));
    surgePolygons = {};
}

function surgeColor(m) {
    if (m >= 2.5) return '#cc0000';
    if (m >= 2.0) return '#ff6600';
    if (m >= 1.5) return '#ffaa00';
    return '#ffdd55';
}

// Congested edges, colored and widened by congestion factor.
function updateCongestion(edges) {
    const seen = new Set();
    (edges || []).forEach(e => {
        seen.add(e.edge_id);
        const color = congestionColor(e.factor);
        const latlngs = [[e.from_lat, e.from_lon], [e.to_lat, e.to_lon]];
        if (congestionLines[e.edge_id]) {
            congestionLines[e.edge_id].setLatLngs(latlngs);
            congestionLines[e.edge_id].setStyle({ color, weight: congestionWeight(e.factor) });
        } else {
            const line = L.polyline(latlngs, {
                color,
                weight: congestionWeight(e.factor),
                opacity: 0.75,
            }).addTo(map);
            line.bindTooltip(`edge ${e.edge_id}: ${e.factor.toFixed(2)}x, ${e.density}/${e.capacity.toFixed(0)} vehicles`);
            congestionLines[e.edge_id] = line;
        }
    });
    Object.keys(congestionLines).forEach(id => {
        if (!seen.has(Number(id))) {
            map.removeLayer(congestionLines[id]);
            delete congestionLines[id];
        }
    });
}

function clearCongestion() {
    Object.values(congestionLines).forEach(line => map.removeLayer(line));
    congestionLines = {};
}

function congestionColor(factor) {
    if (factor >= 2.0) return '#ff2222';
    if (factor >= 1.5) return '#ff7700';
    if (factor >= 1.2) return '#ffcc00';
    return '#88dd44';
}

function congestionWeight(factor) {
    return Math.min(3 + (factor - 1) * 3, 9);
}

function updateMetrics(metrics) {
    document.getElementById('total-rides').textContent = metrics.total_rides || 0;
    document.getElementById('active-rides').textContent = metrics.active_rides || 0;
    document.getElementById('pending-requests').textContent = metrics.pending_requests || 0;
    document.getElementById('abandoned').textContent = metrics.abandoned || 0;

    document.getElementById('avg-wait').textContent = (metrics.avg_wait_time || 0).toFixed(1) + 's';
    document.getElementById('p95-wait').textContent = (metrics.p95_wait_time || 0).toFixed(1) + 's';
    document.getElementById('throughput').textContent = (metrics.throughput || 0).toFixed(1) + ' rides/hr';

    const avgCongestion = metrics.avg_congestion || 1.0;
    const maxCongestion = metrics.max_congestion || 1.0;

    document.getElementById('avg-congestion').textContent = avgCongestion.toFixed(2) + 'x';
    document.getElementById('max-congestion').textContent = maxCongestion.toFixed(2) + 'x';
    document.getElementById('congested-edges').textContent = metrics.congested_edges || 0;

    const avgCongestionEl = document.getElementById('avg-congestion');
    const maxCongestionEl = document.getElementById('max-congestion');

    if (avgCongestion < 1.1) {
        avgCongestionEl.className = 'metric-value good';
    } else if (avgCongestion < 1.5) {
        avgCongestionEl.className = 'metric-value warning';
    } else {
        avgCongestionEl.className = 'metric-value danger';
    }

    if (maxCongestion < 1.5) {
        maxCongestionEl.className = 'metric-value good';
    } else if (maxCongestion < 2.0) {
        maxCongestionEl.className = 'metric-value warning';
    } else {
        maxCongestionEl.className = 'metric-value danger';
    }
}

function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws`;

    showConnectionStatus('Connecting', false);
    ws = new WebSocket(wsUrl);

    ws.onopen = function() {
        showConnectionStatus('Connected', true);
    };

    ws.onmessage = function(event) {
        const data = JSON.parse(event.data);
        handleUpdate(data);
    };

    ws.onerror = function(error) {
        console.error('WebSocket error:', error);
        showConnectionStatus('Connection Error', false);
    };

    ws.onclose = function() {
        if (stopped) return;
        showConnectionStatus('Disconnected, reconnecting', false);
        setTimeout(connectWebSocket, 2000);
    };
}

// Frames come from cmd/metrosim ({type, tick, vehicles, congestion, hexes,
// surge, metrics, paused, speed}) or from cmd/live-view ({time, vehicles,
// surge, counters}), which has no controls or per-trip metrics. Go leaves
// empty lists out of the JSON, so a missing list means an empty one.
function handleUpdate(data) {
    if (typeof data.tick === 'undefined') {
        document.body.classList.add('liveview');
    } else {
        document.getElementById('tick').textContent = data.tick;
    }
    if (data.type === 'initial') {
        applyControlState(data);
    }

    const vehicles = data.vehicles || [];
    document.getElementById('vehicle-count').textContent = vehicles.length;
    const seen = new Set();
    const counts = { idle: 0, repositioning: 0, enroute: 0, assigned: 0 };
    vehicles.forEach(v => {
        seen.add(v.id);
        updateVehicle(v);
        if (counts[v.state] !== undefined) counts[v.state]++;
    });
    document.getElementById('legend-idle').textContent = counts.idle;
    document.getElementById('legend-repositioning').textContent = counts.repositioning;
    document.getElementById('legend-enroute').textContent = counts.enroute;
    document.getElementById('legend-assigned').textContent = counts.assigned;
    Object.keys(vehicleMarkers).forEach(id => {
        if (!seen.has(Number(id))) {
            map.removeLayer(vehicleMarkers[id]);
            delete vehicleMarkers[id];
        }
    });
    if (!mapRecentered && vehicles.length > 0) {
        map.fitBounds(fleetBounds(vehicles), { padding: [30, 30], maxZoom: 16 });
        mapRecentered = true;
    }

    if (congestionOverlayOn) updateCongestion(data.congestion || []);
    if (hexOverlayOn) updateHexes(data.hexes || []);
    if (surgeOverlayOn) updateSurge(data.surge || []);
    if (data.metrics) updateMetrics(data.metrics);
    if (data.counters) updateCounters(data.counters);
}

function applyControlState(data) {
    if (typeof data.speed === 'number' && data.speed > 0) {
        document.getElementById('speed-slider').value = data.speed;
        document.getElementById('speed-value').textContent = data.speed.toFixed(1) + 'x';
    }
    paused = data.paused === true;
    document.getElementById('btn-pause').textContent = paused ? 'Resume' : 'Pause';
}

// live-view derives the queue and ride counts from trip events.
function updateCounters(c) {
    document.getElementById('requested').textContent = c.requested || 0;
    document.getElementById('pending-requests').textContent = c.pending || 0;
    document.getElementById('active-rides').textContent = c.active || 0;
    document.getElementById('abandoned').textContent = c.abandoned || 0;
    document.getElementById('total-rides').textContent = c.completed || 0;
}

function showConnectionStatus(message, isConnected) {
    const statusEl = document.getElementById('connection-status');
    statusEl.textContent = message;
    statusEl.className = 'connection-status' + (isConnected ? ' connected' : '');
    statusEl.style.display = 'block';

    if (isConnected) {
        setTimeout(() => {
            statusEl.style.display = 'none';
        }, 3000);
    }
}

function sendControl(action, value) {
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({
            action: action,
            value: value
        }));
    }
}

function initControls() {
    const pauseBtn = document.getElementById('btn-pause');
    const stopBtn = document.getElementById('btn-stop');
    const speedSlider = document.getElementById('speed-slider');
    const speedValue = document.getElementById('speed-value');

    pauseBtn.addEventListener('click', () => {
        paused = !paused;
        sendControl(paused ? 'pause' : 'resume');
        pauseBtn.textContent = paused ? 'Resume' : 'Pause';
    });

    stopBtn.addEventListener('click', () => {
        if (!confirm('Stop the simulation? It cannot be restarted from here.')) return;
        sendControl('stop');
        stopped = true;
        [pauseBtn, stopBtn, speedSlider].forEach(el => { el.disabled = true; });
        showConnectionStatus('Simulation stopped', false);
    });

    speedSlider.addEventListener('input', (e) => {
        const speed = parseFloat(e.target.value);
        speedValue.textContent = speed.toFixed(1) + 'x';
    });

    speedSlider.addEventListener('change', (e) => {
        const speed = parseFloat(e.target.value);
        sendControl('speed', speed);
    });

    const congBtn = document.getElementById('btn-congestion-toggle');
    congBtn.addEventListener('click', () => {
        congestionOverlayOn = !congestionOverlayOn;
        congBtn.textContent = congestionOverlayOn ? 'Hide congestion edges' : 'Show congestion edges';
        if (!congestionOverlayOn) clearCongestion();
    });

    const hexBtn = document.getElementById('btn-hex-toggle');
    hexBtn.addEventListener('click', () => {
        hexOverlayOn = !hexOverlayOn;
        hexBtn.textContent = hexOverlayOn ? 'Hide driver hex (r9)' : 'Show driver hex (r9)';
        if (!hexOverlayOn) clearHexes();
    });

    const surgeBtn = document.getElementById('btn-surge-toggle');
    surgeBtn.addEventListener('click', () => {
        surgeOverlayOn = !surgeOverlayOn;
        surgeBtn.textContent = surgeOverlayOn ? 'Hide surge hex (r8)' : 'Show surge hex (r8)';
        if (!surgeOverlayOn) clearSurge();
    });
}

function init() {
    initMap();
    initControls();
    connectWebSocket();
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}

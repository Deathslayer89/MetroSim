package tracelog

/**
 * seenRuns is the trace writer's dedup set, grouped by run. It keeps whole runs
 * in memory up to capacity keys and evicts the least recently used run. A run
 * that was evicted is read back from the files that hold it the next time one
 * of its trips arrives, so eviction costs a reload, never a duplicate.
 */
type seenRuns struct {
	capacity int
	resident map[string]map[tripKey]struct{}
	order    []string // resident runs, least recently used first
	size     int
	files    map[string]map[string]struct{} // run to the files holding its rows
	load     func(path string) ([]tripKey, error)
}

func newSeenRuns(capacity int, load func(string) ([]tripKey, error)) *seenRuns {
	if capacity <= 0 {
		capacity = 1
	}
	return &seenRuns{
		capacity: capacity,
		resident: make(map[string]map[tripKey]struct{}),
		files:    make(map[string]map[string]struct{}),
		load:     load,
	}
}

func (s *seenRuns) has(k tripKey) (bool, error) {
	keys, err := s.ensure(k.run())
	if err != nil {
		return false, err
	}
	_, ok := keys[k]
	return ok, nil
}

// add records that k is on disk in file.
func (s *seenRuns) add(k tripKey, file string) error {
	run := k.run()
	keys, err := s.ensure(run)
	if err != nil {
		return err
	}
	if s.files[run] == nil {
		s.files[run] = make(map[string]struct{})
	}
	s.files[run][file] = struct{}{}
	if _, ok := keys[k]; !ok {
		keys[k] = struct{}{}
		s.size++
	}
	s.evict(run)
	return nil
}

// ensure makes run resident, reading it back from disk if it was evicted.
func (s *seenRuns) ensure(run string) (map[tripKey]struct{}, error) {
	if keys, ok := s.resident[run]; ok {
		s.touch(run)
		return keys, nil
	}
	keys := make(map[tripKey]struct{})
	for path := range s.files[run] {
		all, err := s.load(path)
		if err != nil {
			return nil, err
		}
		for _, k := range all {
			if k.run() == run {
				keys[k] = struct{}{}
			}
		}
	}
	s.resident[run] = keys
	s.order = append(s.order, run)
	s.size += len(keys)
	s.evict(run)
	return keys, nil
}

func (s *seenRuns) touch(run string) {
	for i, r := range s.order {
		if r == run {
			copy(s.order[i:], s.order[i+1:])
			s.order[len(s.order)-1] = run
			return
		}
	}
}

// evict drops least recently used runs other than keep until size fits capacity.
func (s *seenRuns) evict(keep string) {
	for s.size > s.capacity && len(s.order) > 1 {
		victim := s.order[0]
		if victim == keep {
			victim = s.order[1]
			s.order = append(s.order[:1], s.order[2:]...)
		} else {
			s.order = s.order[1:]
		}
		s.size -= len(s.resident[victim])
		delete(s.resident, victim)
	}
}

func (s *seenRuns) len() int { return s.size }

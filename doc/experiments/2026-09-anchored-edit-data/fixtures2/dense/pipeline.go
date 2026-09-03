package pipe

import "fmt"

type Stage func([]byte) ([]byte, error)

func Run(in []byte, stages []Stage) ([]byte, error) {
	cur := in
	for i, s := range stages {
		out, err := s(cur)
		if err != nil {
			return nil, fmt.Errorf("stage %d: %w", i, err)
		}
		cur = out
	}
	return cur, nil
}

func RunNamed(in []byte, names []string, stages []Stage) ([]byte, error) {
	cur := in
	for i, s := range stages {
		out, err := s(cur)
		if err != nil {
			return nil, fmt.Errorf("stage %d: %w", i, err)
		}
		cur = out
	}
	return cur, nil
}

func RunUntil(in []byte, stop int, stages []Stage) ([]byte, error) {
	cur := in
	for i, s := range stages {
		if i >= stop {
			break
		}
		out, err := s(cur)
		if err != nil {
			return nil, fmt.Errorf("stage %d: %w", i, err)
		}
		cur = out
	}
	return cur, nil
}

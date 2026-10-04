//go:build !linux && !darwin && !windows

package ptree

func snapshot() func(int) (proc, bool) { return func(int) (proc, bool) { return proc{}, false } }

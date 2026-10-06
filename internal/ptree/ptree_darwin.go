package ptree

import "golang.org/x/sys/unix"

func snapshot() func(int) (proc, bool) {
	return func(pid int) (proc, bool) {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || kp.Proc.P_pid == 0 {
			return proc{}, false
		}
		return proc{PPID: int(kp.Eproc.Ppid), Name: unix.ByteSliceToString(kp.Proc.P_comm[:]), Zombie: kp.Proc.P_stat == 5}, true // 5 = SZOMB
	}
}

package ptree

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func snapshot() func(int) (proc, bool) {
	m := map[int]proc{}
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err == nil {
		defer windows.CloseHandle(h)
		var e windows.ProcessEntry32
		e.Size = uint32(unsafe.Sizeof(e))
		for err := windows.Process32First(h, &e); err == nil; err = windows.Process32Next(h, &e) {
			m[int(e.ProcessID)] = proc{PPID: int(e.ParentProcessID), Name: windows.UTF16ToString(e.ExeFile[:])}
		}
	}
	return func(pid int) (proc, bool) { p, ok := m[pid]; return p, ok }
}

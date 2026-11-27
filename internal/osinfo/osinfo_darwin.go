package osinfo

import (
	"golang.org/x/sys/unix"
)

// describe asks the kernel, not SystemVersion.plist: under
// SYSTEM_VERSION_COMPAT=1 the plist reads 10.16 on macOS 11 and later, which
// is the trap CPython's mac_ver falls into. kern.osproductversion has been
// there since 10.13.4. Under Rosetta, uname reports x86_64, so the real
// machine comes from sysctl.proc_translated.
func describe() string {
	version, _ := unix.Sysctl("kern.osproductversion")
	machine, emulated := "", ""
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		machine = unix.ByteSliceToString(u.Machine[:])
	}
	if t, err := unix.SysctlUint32("sysctl.proc_translated"); err == nil && t == 1 {
		machine, emulated = "arm64", machine
	}
	return MacOS(version, machine, emulated)
}

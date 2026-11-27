package osinfo

import (
	"runtime"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// describe takes the version from RtlGetVersion, which is not subject to the
// compatibility shims that make GetVersionEx report whatever the manifest
// asks for (CPython runs `ver` for the same reason), and the name from the
// registry. IsWow64Process2 gives the native machine, which differs from the
// build's under x64 emulation on ARM64 or WOW64.
func describe() string {
	v := windows.RtlGetVersion()
	var product, display string
	var ubr uint32
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		product, _, _ = k.GetStringValue("ProductName")
		display, _, _ = k.GetStringValue("DisplayVersion")
		if n, _, err := k.GetIntegerValue("UBR"); err == nil {
			ubr = uint32(n)
		}
		_ = k.Close()
	}
	machine, emulated := goarchName(runtime.GOARCH), ""
	var proc, native uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &proc, &native); err == nil {
		if n := machineName(native); n != "" && n != machine {
			machine, emulated = n, goarchName(runtime.GOARCH)
		}
	}
	return Windows(product, display, v.BuildNumber, ubr, machine, emulated)
}

// machineName maps an IMAGE_FILE_MACHINE value to the name Windows uses.
func machineName(m uint16) string {
	switch m {
	case 0x8664:
		return "AMD64"
	case 0xAA64:
		return "ARM64"
	case 0x014c:
		return "x86"
	}
	return ""
}

func goarchName(a string) string {
	switch a {
	case "amd64":
		return "AMD64"
	case "arm64":
		return "ARM64"
	case "386":
		return "x86"
	}
	return a
}

package osinfo

import (
	"runtime"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"ubuntu", "PRETTY_NAME=\"Ubuntu 24.04.5 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\n", "Ubuntu 24.04.5 LTS"},
		{"fedora", "NAME=\"Fedora Linux\"\nVERSION=\"41 (Workstation Edition)\"\nID=fedora\nPRETTY_NAME=\"Fedora Linux 41 (Workstation Edition)\"\n", "Fedora Linux 41 (Workstation Edition)"},
		{"arch, no version", "NAME=\"Arch Linux\"\nPRETTY_NAME=\"Arch Linux\"\nID=arch\nBUILD_ID=rolling\n", "Arch Linux"},
		{"alpine, unquoted id", "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.21.0\nPRETTY_NAME=\"Alpine Linux v3.21\"\n", "Alpine Linux v3.21"},
		{"single quotes", "PRETTY_NAME='NixOS 24.11 (Vicuna)'\n", "NixOS 24.11 (Vicuna)"},
		{"escapes", `PRETTY_NAME="Acme \"Edge\" \$5 \\ \` + "`" + `x\` + "`" + `"` + "\n", "Acme \"Edge\" $5 \\ `x`"},
		{"no pretty name", "NAME=Gentoo\nVERSION=2.17\n", "Gentoo 2.17"},
		{"nothing usable", "# a comment\n  PRETTY_NAME=indented\nnot a line\n", "Linux"},
		{"crlf", "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\r\n", "Debian GNU/Linux 12 (bookworm)"},
		{"unbalanced quote is kept", "PRETTY_NAME=\"Odd\n", "\"Odd"},
	} {
		if got := DistroName(ParseOSRelease(tc.text)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWSL(t *testing.T) {
	for kernel, want := range map[string]string{
		"4.4.0-19041-Microsoft":              "WSL1",
		"5.15.167.4-microsoft-standard-WSL2": "WSL2",
		"6.6.36.3-microsoft-custom":          "WSL2",
		"6.18.44-fc-v880":                    "",
		"6.8.0-49-generic":                   "",
	} {
		if got := WSL(kernel); got != want {
			t.Errorf("WSL(%q) = %q, want %q", kernel, got, want)
		}
	}
	got := Linux(ParseOSRelease(`PRETTY_NAME="Ubuntu 24.04.1 LTS"`), "5.15.167.4-microsoft-standard-WSL2", "x86_64")
	if want := "Ubuntu 24.04.1 LTS under WSL2 on Windows (x86_64)"; got != want {
		t.Errorf("Linux on WSL2 = %q, want %q", got, want)
	}
}

func TestWindowsName(t *testing.T) {
	for _, tc := range []struct {
		product string
		build   uint32
		want    string
	}{
		{"Windows 10 Pro", 19045, "Windows 10 Pro"},
		{"Windows 10 Pro", 22000, "Windows 11 Pro"},
		{"Windows 10 Enterprise", 26100, "Windows 11 Enterprise"},
		{"Windows Server 2022 Datacenter", 20348, "Windows Server 2022 Datacenter"},
		{"Windows Server 2025 Datacenter", 26100, "Windows Server 2025 Datacenter"},
		{"", 22631, "Windows"},
	} {
		if got := WindowsName(tc.product, tc.build); got != tc.want {
			t.Errorf("WindowsName(%q, %d) = %q, want %q", tc.product, tc.build, got, tc.want)
		}
	}
	if got, want := Windows("Windows 10 Pro", "23H2", 22631, 4460, "ARM64", "AMD64"),
		"Windows 11 Pro 23H2 (build 22631.4460, ARM64; Strument is running as AMD64 under emulation)"; got != want {
		t.Errorf("Windows = %q, want %q", got, want)
	}
}

func TestMacOS(t *testing.T) {
	if got, want := MacOS("15.2", "arm64", "x86_64"), "macOS 15.2 (arm64; Strument is running as x86_64 under emulation)"; got != want {
		t.Errorf("MacOS = %q, want %q", got, want)
	}
	if got, want := MacOS("", "", ""), "macOS"; got != want {
		t.Errorf("MacOS with nothing read = %q, want %q", got, want)
	}
}

// On the machine running the test, Describe says something more than the
// build target where it can read the system.
func TestDescribeHere(t *testing.T) {
	got := Describe()
	t.Logf("Describe() = %q", got)
	if got == "" {
		t.Fatal("empty description")
	}
	if (runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows") && got == runtime.GOOS+"-"+runtime.GOARCH {
		t.Errorf("fell back to the build target on %s", runtime.GOOS)
	}
}

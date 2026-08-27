package coder

import (
	"testing"
	"time"

	"dbohdan.com/strument/internal/config"
)

// TestPlatformDateFollowsTheSessionZone is the other end of env_set's TZ.
// config.ApplyTimeZone moves time.Local; this is the claim that moving it
// reaches the one date the model is shown, "Current date" in the system prompt.
//
// The assertion is that two zones give two different answers, rather than that
// one zone gives an expected one. Comparing Date against
// time.Now().In(loc).Format(...) would restate the implementation and pass just
// as happily if Date were switched to UTC. Kiritimati and Midway are 25 hours
// apart, so their local dates always differ — no clock-dependent flake, and a
// Date built from anything but the session's zone gives the same string twice.
func TestPlatformDateFollowsTheSessionZone(t *testing.T) {
	saved := time.Local                      //nolint:gosmopolitan // Saving the process zone to restore it.
	t.Cleanup(func() { time.Local = saved }) //nolint:gosmopolitan // Restoring it.

	m := &config.Model{Slug: "test"}
	m.SideModel = m

	dateIn := func(zone string) string {
		t.Helper()
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skipf("no %s in the zone database: %v", zone, err)
		}
		time.Local = loc //nolint:gosmopolitan // Setting the session zone is the subject.
		return New(t.TempDir(), m).Platform.Date
	}

	east := dateIn("Pacific/Kiritimati") // UTC+14
	west := dateIn("Pacific/Midway")     // UTC-11
	if east == west {
		t.Errorf("Current date is %q in both UTC+14 and UTC-11; it is not using the session's zone", east)
	}
}

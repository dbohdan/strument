"""Build the fixture: Larkspur, a small invented project.

Larkspur schedules crop rotation for a community garden's beds. It is
fiction, built to give a session two kinds of work: reading long,
hard-wrapped design notes (the GLM session that prompted this trial was
doing that when it lost track of its tools) and fixing a failing Go test,
which gives a reason to run commands.

Usage: python3 fixture.py DEST
"""

import os
import subprocess
import sys
import textwrap


def wrap(paragraphs):
    return "\n\n".join(textwrap.fill(p, 78) for p in paragraphs) + "\n"


FAMILIES = ["brassicas", "legumes", "roots", "alliums", "nightshades", "cucurbits"]

NOTES_ROTATION = "# How Larkspur rotates beds\n\n" + wrap([
    "Every bed in the garden belongs to one plot holder for a season, and every "
    "season each bed is planted with one crop family. The rotation rule exists "
    "because a family grown in the same soil two seasons running draws down the "
    "same nutrients and carries its pests and diseases forward into the next "
    "planting.",
    "Larkspur keeps a fixed order of six families: brassicas, legumes, roots, "
    "alliums, nightshades, cucurbits. A bed moves one step along this order each "
    "season. After cucurbits it returns to brassicas, so the cycle takes six "
    "seasons to come back to where it began.",
    "Legumes follow brassicas on purpose. Brassicas are heavy feeders, and "
    "legumes fix nitrogen, so the bed recovers during the season that follows the "
    "heaviest demand on it. Roots follow legumes because they do poorly in soil "
    "that is too rich, and a season of legumes leaves the bed moderate rather than "
    "rich.",
    "A bed may be rested. A rested bed is planted with a cover crop and does not "
    "advance in the order that season: the next season it takes the family it "
    "would have taken had it not been rested. Resting is how the committee handles "
    "a bed whose soil tests poorly, and it is recorded per bed and per season.",
    "The committee can also pin a bed. A pinned bed keeps the same family for as "
    "long as the pin stands, which is how the perennial asparagus and rhubarb beds "
    "are handled. Pins are rare, and the rotation report lists them separately so "
    "that nobody mistakes a pinned bed for one the rule has failed to move.",
] * 3)

NOTES_HISTORY = "# Version history\n\n" + wrap([
    "Version 1 rotated beds in a four-family order: brassicas, legumes, roots, "
    "alliums. Nightshades and cucurbits were grown only in the two greenhouse "
    "beds, which were outside the rotation altogether.",
    "Version 2 brought the greenhouse beds into the rotation and extended the "
    "order to six families. The change followed two seasons of blight in the "
    "greenhouse, which the committee traced to tomatoes grown in the same soil "
    "four years in a row.",
    "Version 2 also introduced resting. In version 1 a bed with poor soil was "
    "simply skipped in the report, which meant it advanced anyway and came back "
    "to the wrong family. The version 2 notes call this the most common complaint "
    "from plot holders in the first three years.",
    "Pins were added in version 2.1, after the asparagus bed was rotated to "
    "legumes by mistake and had to be replanted from crowns.",
] * 3)

GO_MOD = "module larkspur\n\ngo 1.22\n"

ROTATION_GO = '''package rotation

// Families is the rotation order. A bed advances one step per season.
var Families = []string{"brassicas", "legumes", "roots", "alliums", "nightshades", "cucurbits"}

// Next returns the family a bed takes next season, given this season's
// family and whether the bed is resting this season. A resting bed does not
// advance.
func Next(current string, resting bool) string {
	for i, f := range Families {
		if f == current {
			if resting {
				return f
			}
			return Families[(i+1)%(len(Families)-1)]
		}
	}
	return Families[0]
}
'''

ROTATION_TEST_GO = '''package rotation

import "testing"

func TestNext(t *testing.T) {
	cases := []struct {
		current string
		resting bool
		want    string
	}{
		{"brassicas", false, "legumes"},
		{"legumes", false, "roots"},
		{"alliums", false, "nightshades"},
		{"nightshades", false, "cucurbits"},
		{"cucurbits", false, "brassicas"},
		{"roots", true, "roots"},
		{"unknown", false, "brassicas"},
	}
	for _, c := range cases {
		if got := Next(c.current, c.resting); got != c.want {
			t.Errorf("Next(%q, %v) = %q, want %q", c.current, c.resting, got, c.want)
		}
	}
}
'''

README = "# Larkspur\n\n" + wrap([
    "Crop rotation for the Larkspur Lane community garden. The rules are in "
    "docs/rotation.md and the history of the rules in docs/history.md. The code "
    "is in rotation/.",
])


def main():
    dest = sys.argv[1]
    files = {
        "README.md": README,
        "docs/rotation.md": NOTES_ROTATION,
        "docs/history.md": NOTES_HISTORY,
        "go.mod": GO_MOD,
        "rotation/rotation.go": ROTATION_GO,
        "rotation/rotation_test.go": ROTATION_TEST_GO,
    }
    for rel, text in files.items():
        path = os.path.join(dest, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(text)
    # The plant has to change the behavior, not only the text: the test must
    # fail on the bug, and only on the bug.
    out = subprocess.run(["go", "test", "./..."], cwd=dest, capture_output=True, text=True)
    assert out.returncode != 0 and "cucurbits" in out.stdout, out.stdout + out.stderr


if __name__ == "__main__":
    main()

package document

import (
	"slices"
	"testing"
)

const guide = "# Updating the gateway\n\nKeep the power on.\n\n## Before you start\n\nBack up the settings.\n\n" +
	"```sh\n# not a heading\nwrenctl backup\n```\n\n## Upload the image\n\n### From the console\n\nOpen System > Firmware.\n\n" +
	"### From the shell\n\nRun wrenctl firmware apply.\n\n## Afterwards\n\nCheck the version."

func TestASectionOpensByItsHeading(t *testing.T) {
	for _, c := range []struct{ heading, want string }{
		{"Upload the image", "## Upload the image\n\n### From the console\n\nOpen System > Firmware.\n\n### From the shell\n\nRun wrenctl firmware apply."},
		// a heading with its hashes and in other case
		{"### from the SHELL", "### From the shell\n\nRun wrenctl firmware apply."},
		// a part of it
		{"afterw", "## Afterwards\n\nCheck the version."},
	} {
		if got, ok := Section(guide, c.heading); !ok || got != c.want {
			t.Errorf("%q: %q %v", c.heading, got, ok)
		}
	}

	for _, missing := range []string{"Downgrade", "not a heading", "", "#"} {
		if got, ok := Section(guide, missing); ok {
			t.Errorf("%q opened %q", missing, got)
		}
	}
}

func TestTheOutlineSkipsFencedBlocks(t *testing.T) {
	want := []string{"# Updating the gateway", "## Before you start", "## Upload the image", "### From the console", "### From the shell", "## Afterwards"}
	if got := Outline(guide); !slices.Equal(got, want) {
		t.Errorf("outline %q", got)
	}
}

func TestTheTitleIsTheFirstHeading(t *testing.T) {
	for text, want := range map[string]string{
		"Intro.\n\n## Restore a file ##\n\n# Later": "Restore a file",
		"```\n# not a heading\n```\n# Real":         "Real",
		"No headings here.":                         "",
	} {
		if got := Title(text); got != want {
			t.Errorf("%q: %q", text, got)
		}
	}
}

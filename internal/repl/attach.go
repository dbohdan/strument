package repl

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/render"
)

// cmdAttach stages images to ride on the next message.
//
// An image is the one file content that rides in a user message. A text file
// the model reads itself, as a tool result — a measured experiment
// (doc/experiments/2026-08-add-instruct/README.md) is why file contents do not
// ride in a fabricated user turn — but an image has no such route: there is no
// text for the model to read instead, and the user attaching a screenshot
// really did attach it, so a user-role image block is the honest encoding
// rather than something the harness made up in their voice.
//
// Bare reports rather than staging or sending, the house pattern /commits,
// /model, /env and /context all follow.
//
// The inverse is `/attach drop`, not `/detach`: `detach` is a session verb in
// a terminal, where screen and tmux have owned the word for decades.
func cmdAttach(_ context.Context, r *REPL, args string) string {
	fields := splitArgs(args)
	if len(fields) > 0 && fields[0] == "drop" {
		return attachDrop(r, fields[1:])
	}
	if len(fields) == 0 {
		r.printAttachments()
		return ""
	}

	for _, path := range fields {
		before := labels(r.coder.Attachments())
		src, err := r.coder.AttachFile(path)
		var dup coder.AlreadyAttachedError
		switch {
		case errors.As(err, &dup):
			if filepath.Base(path) == filepath.Base(dup.Label) {
				r.printf("%s is already attached.", dup.Label)
			} else {
				r.printf("%s is the same image as %s, which is already attached.", path, dup.Label)
			}
			continue
		case err != nil:
			r.out.Errorf("Could not attach %s: %v", path, err)
			continue
		default:
			r.printf("Attached %s.", src.String())
		}
		// A twin's arrival lengthens the earlier label; say so, or a later
		// /attach drop by the old name would match both.
		for _, l := range labels(r.coder.Attachments()) {
			if l == src.Label || slices.Contains(before, l) {
				continue
			}
			// Which word fits is decided by what just arrived: a new version
			// of the same file numbers the earlier one, a different file with
			// the same name lengthens the other's label.
			name, _, _ := strings.Cut(filepath.Base(l), "#")
			if strings.Contains(src.Label, "#") && !strings.Contains(l, "/") {
				r.printf("The earlier copy of %s is now %s.", name, l)
			} else {
				r.printf("The other %s is now %s.", name, l)
			}
		}
	}
	r.warnIfModelCannotSee()
	return ""
}

func attachDrop(r *REPL, names []string) string {
	if len(names) == 0 {
		if n := r.coder.DropAttachments(); n == 0 {
			r.printf("Nothing was attached.")
		} else {
			r.printf("Removed %s from your next message.", render.Plural(n, "image", "images"))
		}
		return ""
	}
	for _, name := range names {
		switch n := r.coder.DropAttachments(name); n {
		case 0:
			r.out.Warningf("No attachment matched %q.", name)
		case 1:
			r.printf("Removed %s from your next message.", name)
		default:
			r.printf("Removed %d images matching %s from your next message.", n, name)
		}
	}
	return ""
}

// printAttachments says what is staged, because an attachment nobody remembers
// staging is a surprise on the next request and a surprise on the bill.
func (r *REPL) printAttachments() {
	staged := r.coder.Attachments()
	if len(staged) == 0 {
		r.printf("No images are attached. Attached images are sent with your next message.")
		return
	}
	r.printf("Attached to your next message:")
	for _, src := range staged {
		r.printf("  %s", src.String())
	}
	r.warnIfModelCannotSee()
}

// warnIfModelCannotSee says so now rather than letting the user find out from
// the model's reply.
//
// The send path projects the image to text regardless, so this is not what
// keeps the request valid — it is only what keeps the user informed at the
// moment they can still act on it, by switching models before they type.
func (r *REPL) warnIfModelCannotSee() {
	if len(r.coder.Attachments()) == 0 {
		return
	}
	m := r.coder.Model
	if m != nil && m.Accepts(llm.BlockImage) {
		return
	}
	name := "This model"
	if m != nil {
		// ReadableName, not DisplayName: display_name is optional, and an
		// unset one left the sentence starting mid-air with " does not accept
		// images". Found by driving the binary, not by a test.
		name = m.ReadableName()
	}
	r.out.Warningf("%s does not accept images, so it will be told the image is there but unavailable.", name)
	r.printf("  Switch with /model, or set input_modalities = [\"text\", \"image\"] if it does accept them.")
}

// labels lists the staged images' labels.
func labels(staged []llm.ImageSource) []string {
	out := make([]string, len(staged))
	for i, s := range staged {
		out[i] = s.Label
	}
	return out
}

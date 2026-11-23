package coder

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/workspace"
)

// errIsADirectory is separate only so the message stays a fragment: every
// caller of AttachFile names the file it was given, so an error that names it
// again reads as "shot.png: shot.png: ...".
var errIsADirectory = errors.New("it is a directory")

// maxAttachBytes caps one attachment.
//
// Refusing rather than downscaling, for the reason /submit gives about
// truncation: a silently shrunk image is worse than one the user resizes
// themselves, because the model reads a blurred screenshot as the screenshot.
// Providers differ on the ceiling — Anthropic rejects images over about 5 MB —
// and base64 adds a third on top, so this is a local refusal with a message the
// user can act on rather than a provider error they cannot.
const maxAttachBytes = 5 << 20

// maxAttachments caps how many ride on one message. A limit that exists to
// catch a mistaken glob, not to ration: eight screenshots is already an unusual
// question, and eighty is a typo with a bill attached.
const maxAttachments = 8

// attachment is a staged image and what tells it apart from the others: the
// file it was read from, and its bytes' digest.
type attachment struct {
	llm.ImageSource

	path string
	sum  [sha256.Size]byte
}

// AlreadyAttachedError refuses an image whose bytes are already staged. Two
// copies would only show the model the same picture twice, at twice the cost,
// and leave /attach drop no way to name one of them.
type AlreadyAttachedError struct {
	Label string // the staged copy's label
}

func (e AlreadyAttachedError) Error() string {
	return "the same image is already attached as " + e.Label
}

// Attachments returns the images staged for the next message, in the order they
// were attached.
func (c *Coder) Attachments() []llm.ImageSource {
	out := make([]llm.ImageSource, len(c.pendingAttachments))
	for i, a := range c.pendingAttachments {
		out[i] = a.ImageSource
	}
	return out
}

// AttachFile reads an image and stages it for the next message.
//
// Deliberately not subject to the ignore rules the file tools apply, and not
// confined to the project. /attach is a path the user typed, which is the same
// reason /run inherits the full environment and /read-only takes files from
// anywhere: the rule in this codebase is about who caused the read, not which
// call does it. The case that settles it is the ordinary one — a screenshot
// lives in ~/Pictures or /tmp, and a gate that kept attachments inside the
// repository would refuse the only files anyone wants to attach. The model's
// own route into images (the read tool) goes through workspace.Read and is
// gated there, because there the path is the model's choice.
//
// The bytes are read now rather than at send. Reading at send would follow a
// file that changed in between, but then what the user was shown when they
// attached it would be a claim about a file nobody had read.
//
// Attaching never stages a duplicate. The same bytes again, from any path, are
// refused with AlreadyAttachedError. The same file with new bytes replaces its
// staged copy in place, and replaced says so: attaching a screenshot again
// after retaking it means the new one, not both. Two different files with the
// same name get labels long enough to tell apart.
func (c *Coder) AttachFile(path string) (src llm.ImageSource, replaced bool, err error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(c.Root, abs)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return llm.ImageSource{}, false, err
	}
	if info.IsDir() {
		return llm.ImageSource{}, false, errIsADirectory
	}
	if info.Size() > maxAttachBytes {
		return llm.ImageSource{}, false, fmt.Errorf("it is %s, and the attachment limit is %s",
			workspace.HumanBytes(info.Size()), workspace.HumanBytes(maxAttachBytes))
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return llm.ImageSource{}, false, err
	}
	img, reason := workspace.SniffImage(data)
	if reason != "" {
		return llm.ImageSource{}, false, errors.New(reason)
	}

	src = llm.ImageSource{
		MediaType: img.MediaType,
		Data:      base64.StdEncoding.EncodeToString(data),
		// The base name, never the path. An attachment is usually from
		// outside the project -- a screenshot in ~/Pictures -- so displayName
		// would put the user's home directory into the model's context on
		// every attach, and on macOS and Windows it could not relativize at
		// all: /var/folders vs /private/var behind a symlink, and RUNNER~1 vs
		// the long name. The label is for naming the thing in prose, and the
		// file's own name is what the user called it.
		Label:  filepath.Base(abs),
		Width:  img.Width,
		Height: img.Height,
	}
	a := attachment{ImageSource: src, path: abs, sum: sha256.Sum256(data)}

	for _, staged := range c.pendingAttachments {
		if staged.sum == a.sum {
			return llm.ImageSource{}, false, AlreadyAttachedError{Label: staged.Label}
		}
	}
	if i := slices.IndexFunc(c.pendingAttachments, func(s attachment) bool { return s.path == abs }); i >= 0 {
		c.pendingAttachments[i] = a
		c.relabel()
		return c.pendingAttachments[i].ImageSource, true, nil
	}
	if len(c.pendingAttachments) >= maxAttachments {
		return llm.ImageSource{}, false, fmt.Errorf(
			"%d images are already attached, which is the limit for one message", maxAttachments)
	}
	c.pendingAttachments = append(c.pendingAttachments, a)
	c.relabel()
	return c.pendingAttachments[len(c.pendingAttachments)-1].ImageSource, false, nil
}

// relabel gives every staged image the shortest label that tells it apart:
// its file name, and where two different files share one, as many of their
// parent directories as it takes. It runs after every change, so a label
// lengthened for a twin shortens again once the twin is dropped. Only the
// directories that differ are added, never the whole path, for the reason
// the label is a base name to begin with.
func (c *Coder) relabel() {
	for i := range c.pendingAttachments {
		mine := c.pendingAttachments[i].path
		depth := 1
		for ; depth < 64; depth++ {
			label := pathTail(mine, depth)
			if !slices.ContainsFunc(c.pendingAttachments, func(o attachment) bool {
				return o.path != mine && pathTail(o.path, depth) == label
			}) {
				break
			}
		}
		c.pendingAttachments[i].Label = pathTail(mine, depth)
	}
}

// pathTail is the last n elements of path, slash-separated.
func pathTail(path string, n int) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if n > len(parts) {
		n = len(parts)
	}
	return strings.Join(parts[len(parts)-n:], "/")
}

// DropAttachments unstages images by name, or all of them when given no names.
// A name matches an image by its label, its file name, or the end of its path,
// so "shot.png" drops every staged shot.png and "b/shot.png" only that one. It
// returns how many were removed, so the caller can say "no such attachment"
// rather than reporting a silent no-op, and say how many when there were
// several.
func (c *Coder) DropAttachments(names ...string) int {
	if len(names) == 0 {
		n := len(c.pendingAttachments)
		c.pendingAttachments = nil
		return n
	}
	before := len(c.pendingAttachments)
	c.pendingAttachments = slices.DeleteFunc(c.pendingAttachments, func(a attachment) bool {
		return slices.ContainsFunc(names, func(name string) bool {
			name = filepath.ToSlash(filepath.Clean(name))
			return name == a.Label || name == filepath.Base(a.path) ||
				strings.HasSuffix(filepath.ToSlash(a.path), "/"+name) || name == filepath.ToSlash(a.path)
		})
	})
	c.relabel()
	return before - len(c.pendingAttachments)
}

// userMessage builds the turn's user message, consuming whatever is staged.
//
// Attachments belong to one message and are gone after it. They are not session
// state: pinning says what the conversation is about and persists, while an
// attachment is part of a sentence the user said once. That is also why they
// are absent from the resume file — there is nothing to restore, because the
// thing they were attached to has already been sent.
func (c *Coder) userMessage(text string) (llm.Message, []attachment) {
	if len(c.pendingAttachments) == 0 {
		return llm.TextMessage(llm.RoleUser, text), nil
	}
	blocks := make([]llm.ContentBlock, 0, len(c.pendingAttachments)+1)
	if text != "" {
		blocks = append(blocks, llm.TextBlock(text))
	}
	for _, a := range c.pendingAttachments {
		blocks = append(blocks, llm.ImageBlock(a.ImageSource))
	}
	consumed := c.pendingAttachments
	c.pendingAttachments = nil
	return llm.Message{Role: llm.RoleUser, Content: llm.BlocksContent(blocks...)}, consumed
}

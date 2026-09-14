package coder

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

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

// Attachments returns the images staged for the next message, in the order they
// were attached.
func (c *Coder) Attachments() []llm.ImageSource {
	return slices.Clone(c.pendingAttachments)
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
func (c *Coder) AttachFile(path string) (llm.ImageSource, error) {
	if len(c.pendingAttachments) >= maxAttachments {
		return llm.ImageSource{}, fmt.Errorf(
			"%d images are already attached, which is the limit for one message", maxAttachments)
	}

	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(c.Root, abs)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return llm.ImageSource{}, err
	}
	if info.IsDir() {
		return llm.ImageSource{}, errIsADirectory
	}
	if info.Size() > maxAttachBytes {
		return llm.ImageSource{}, fmt.Errorf("it is %s, and the attachment limit is %s",
			workspace.HumanBytes(info.Size()), workspace.HumanBytes(maxAttachBytes))
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return llm.ImageSource{}, err
	}
	img, reason := workspace.SniffImage(data)
	if reason != "" {
		return llm.ImageSource{}, errors.New(reason)
	}

	src := llm.ImageSource{
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
	c.pendingAttachments = append(c.pendingAttachments, src)
	return src, nil
}

// DropAttachments unstages images by label, or all of them when given no names.
// It returns how many were removed, so the caller can say "no such attachment"
// rather than reporting a silent no-op.
func (c *Coder) DropAttachments(names ...string) int {
	if len(names) == 0 {
		n := len(c.pendingAttachments)
		c.pendingAttachments = nil
		return n
	}
	before := len(c.pendingAttachments)
	c.pendingAttachments = slices.DeleteFunc(c.pendingAttachments, func(src llm.ImageSource) bool {
		return slices.ContainsFunc(names, func(name string) bool {
			return name == src.Label || name == filepath.Base(src.Label)
		})
	})
	return before - len(c.pendingAttachments)
}

// userMessage builds the turn's user message, consuming whatever is staged.
//
// Attachments belong to one message and are gone after it. They are not session
// state: pinning says what the conversation is about and persists, while an
// attachment is part of a sentence the user said once. That is also why they
// are absent from the resume file — there is nothing to restore, because the
// thing they were attached to has already been sent.
func (c *Coder) userMessage(text string) (llm.Message, []llm.ImageSource) {
	if len(c.pendingAttachments) == 0 {
		return llm.TextMessage(llm.RoleUser, text), nil
	}
	blocks := make([]llm.ContentBlock, 0, len(c.pendingAttachments)+1)
	if text != "" {
		blocks = append(blocks, llm.TextBlock(text))
	}
	for _, src := range c.pendingAttachments {
		blocks = append(blocks, llm.ImageBlock(src))
	}
	consumed := c.pendingAttachments
	c.pendingAttachments = nil
	return llm.Message{Role: llm.RoleUser, Content: llm.BlocksContent(blocks...)}, consumed
}

package history

import (
	"fmt"
	"os"
	"time"
)

// Pruning stored payloads without forgetting that they existed.
//
// This is the other half of the blob store. Nothing here is ever deleted on
// Strument's own initiative, so a record that keeps every tool result verbatim
// grows without bound and holds whatever the model read out of the project.
// Separating the payloads made them removable; this removes them, and leaves
// the record whole — the hash, the size and the payload's first line stay, so
// the conversation still reads as a conversation and can still be replayed.
//
// A sweep rather than a walk of one session, because blobs are addressed by
// content and shared: a file read in five turns across three conversations is
// one blob, and it may only go when nothing recent still points at it. That
// sharing is also what makes the accidental-secret case converge — one removal
// takes every copy of that content, where a per-reference scheme would leave
// the fifth behind.

// StripPlan is what a sweep would remove, worked out before anything is.
//
// Separate from applying it so the user can be shown the cost first and so a
// test can check the arithmetic without deleting anything.
type StripPlan struct {
	// Remove are the blob hashes to delete, and Bytes their total size.
	Remove []string
	Bytes  int64
	// Keep counts the payloads a recent enough record still points at.
	Keep      int
	KeepBytes int64
	// Orphans counts blobs that no record refers to — left by a deleted
	// session, or by a run that died between storing a payload and recording
	// the row that named it. They are removed whatever the cutoff, because no
	// age can be established for something nothing refers to.
	Orphans int
}

// Empty reports a sweep with nothing to do.
func (p StripPlan) Empty() bool { return len(p.Remove) == 0 }

// PlanStrip works out which payloads a sweep would remove: those whose newest
// reference is older than before, and those with no reference at all.
//
// A reference's age is the modification time of the segment that holds it. A
// segment is one run of Strument, so its mtime is when that conversation last
// had anything happen in it — which is the age a person means. It is also the
// measure `strument project list` already uses, and it fails in the safe
// direction: a copy or a restore that resets mtimes makes everything look
// recent, so the sweep keeps rather than removes.
func PlanStrip(projectRoot string, before time.Time) (StripPlan, error) {
	var plan StripPlan

	blobs, err := ListBlobs(projectRoot)
	if err != nil {
		return plan, err
	}
	if len(blobs) == 0 {
		return plan, nil
	}

	newest, err := newestReferences(projectRoot)
	if err != nil {
		return plan, err
	}

	for _, hash := range blobs {
		size := blobSize(projectRoot, hash)
		when, referenced := newest[hash]
		switch {
		case !referenced:
			plan.Orphans++
			plan.Remove = append(plan.Remove, hash)
			plan.Bytes += size
		case when.Before(before):
			plan.Remove = append(plan.Remove, hash)
			plan.Bytes += size
		default:
			plan.Keep++
			plan.KeepBytes += size
		}
	}
	return plan, nil
}

// newestReferences maps each referenced blob to the newest moment a record
// pointed at it, across every session in the project.
//
// Anything it cannot read fails the whole scan. That is the opposite of how a
// history reader treats damage, and deliberately: this map is used to decide
// what is unreferenced, so a segment skipped here turns every payload it names
// into an orphan, and orphans are removed whatever the cutoff. The mtime rule
// above fails toward keeping; a read error has to as well.
func newestReferences(projectRoot string) (map[string]time.Time, error) {
	sessions, err := ListSessions(projectRoot)
	if err != nil {
		return nil, err
	}
	newest := map[string]time.Time{}
	note := func(hash string, when time.Time) {
		if hash == "" {
			return
		}
		if got, ok := newest[hash]; !ok || when.After(got) {
			newest[hash] = when
		}
	}

	for _, s := range sessions {
		segments, err := LogSegments(projectRoot, s.Name)
		if err != nil {
			return nil, fmt.Errorf("cannot list the records of session %s, so nothing can be shown "+
				"to be unreferenced: %w", s.Name, err)
		}
		for _, seg := range segments {
			info, err := os.Stat(seg)
			if err != nil {
				return nil, fmt.Errorf("cannot read %s, so nothing can be shown to be unreferenced: %w", seg, err)
			}
			records, complete, err := readRecords(seg)
			if err != nil {
				return nil, fmt.Errorf("cannot read %s, so nothing can be shown to be unreferenced: %w", seg, err)
			}
			if !complete {
				return nil, fmt.Errorf("%s has a record that does not decode with more after it, "+
					"so the payloads those rows name cannot be counted; nothing was stripped", seg)
			}
			for _, r := range records {
				note(r.Blob, info.ModTime())
				for _, tc := range r.ToolCalls {
					note(tc.Blob, info.ModTime())
				}
			}
		}
	}
	return newest, nil
}

// ApplyStrip removes the payloads a plan names, and reports what went.
//
// A blob that will not delete is counted as kept rather than failing the
// sweep: the rest of the removals are still worth making, and the one that
// stayed will be offered again next time.
func ApplyStrip(projectRoot string, plan StripPlan) (removed int, freed int64, err error) {
	for _, hash := range plan.Remove {
		size := blobSize(projectRoot, hash)
		if err := DeleteBlob(projectRoot, hash); err != nil {
			continue
		}
		removed++
		freed += size
	}
	return removed, freed, nil
}

func blobSize(projectRoot, hash string) int64 {
	p, err := BlobPath(projectRoot, hash)
	if err != nil {
		return 0
	}
	info, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return info.Size()
}

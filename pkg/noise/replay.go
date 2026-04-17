package noise

import "sync"

const (
	replayWindowBits  = 2048
	replayWindowWords = replayWindowBits / 64
)

// ReplayWindow tracks recently seen counters using a sliding 2048-bit bitmap.
type ReplayWindow struct {
	mu          sync.Mutex
	highestSeen uint64
	initialized bool
	bitmap      [replayWindowWords]uint64
}

// NewReplayWindow returns an empty replay window.
func NewReplayWindow() *ReplayWindow {
	return &ReplayWindow{}
}

// Accept reports whether a counter is fresh, and records it when accepted.
func (rw *ReplayWindow) Accept(counter uint64) bool {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if !rw.initialized {
		rw.initialized = true
		rw.highestSeen = counter
		rw.markDeltaLocked(0)
		return true
	}

	if counter > rw.highestSeen {
		delta := counter - rw.highestSeen
		rw.advanceLocked(delta)
		rw.highestSeen = counter
		rw.markDeltaLocked(0)
		return true
	}

	delta := rw.highestSeen - counter
	if delta >= replayWindowBits {
		return false
	}
	if rw.hasDeltaLocked(delta) {
		return false
	}

	rw.markDeltaLocked(delta)
	return true
}

// HighestSeen returns the latest accepted counter.
func (rw *ReplayWindow) HighestSeen() uint64 {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.highestSeen
}

func (rw *ReplayWindow) hasDeltaLocked(delta uint64) bool {
	word := int(delta / 64)
	bit := uint(delta % 64)
	return (rw.bitmap[word] & (uint64(1) << bit)) != 0
}

func (rw *ReplayWindow) markDeltaLocked(delta uint64) {
	word := int(delta / 64)
	bit := uint(delta % 64)
	rw.bitmap[word] |= uint64(1) << bit
}

func (rw *ReplayWindow) advanceLocked(delta uint64) {
	if delta >= replayWindowBits {
		rw.bitmap = [replayWindowWords]uint64{}
		return
	}

	wordShift := int(delta / 64)
	bitShift := uint(delta % 64)

	if wordShift > 0 {
		for i := replayWindowWords - 1; i >= 0; i-- {
			src := i - wordShift
			if src >= 0 {
				rw.bitmap[i] = rw.bitmap[src]
			} else {
				rw.bitmap[i] = 0
			}
		}
	}

	if bitShift > 0 {
		for i := replayWindowWords - 1; i >= 0; i-- {
			carry := uint64(0)
			if i > 0 {
				carry = rw.bitmap[i-1] >> (64 - bitShift)
			}
			rw.bitmap[i] = (rw.bitmap[i] << bitShift) | carry
		}
	}
}

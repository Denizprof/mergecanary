package board

import (
	"fmt"
	"io"
	"strings"
)

// Redrawer draws successive frames over one another on an ANSI terminal.
type Redrawer struct {
	W io.Writer
}

// Draw replaces the previous frame: cursor home, each line cleared to its end,
// then everything below cleared. It does not depend on line wrapping.
func (r *Redrawer) Draw(frame string) {
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	var b strings.Builder
	b.WriteString("\x1b[H")
	for _, l := range lines {
		b.WriteString(l + "\x1b[K\n")
	}
	b.WriteString("\x1b[J")
	fmt.Fprint(r.W, b.String())
}

// Clear wipes the screen and homes the cursor (used once before the first frame).
func Clear(w io.Writer) { fmt.Fprint(w, "\x1b[2J\x1b[H") }

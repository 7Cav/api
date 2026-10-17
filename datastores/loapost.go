package datastores

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// loaPost holds what an LOA's opening post says. A zero date means the post
// gives no readable date.
type loaPost struct {
	start, end time.Time
	// subject is the text of the post's last Username label, with its
	// markup stripped. hasSubject reports whether the post has one.
	subject    string
	hasSubject bool
	// mentionID is the user id of the member that label mentions, or 0
	// when the label is typed text.
	mentionID uint64
	// reason is the text under the post's reason label as plain text, or
	// "" when the post has no reason label.
	reason string
}

var (
	// bbcodeTag matches one opening or closing BBCode tag, with or without
	// an option, such as [B], [/COLOR], [COLOR=rgb(213, 185, 0)], [USER=9001]
	// or [*].
	bbcodeTag = regexp.MustCompile(`(?i)\[/?[a-z*][a-z0-9*]*(?:=[^\]\n]*)?\]`)
	// struckText matches struck-through text and its [S] tags.
	struckText = regexp.MustCompile(`(?is)\[s\].*?\[/s\]`)
	// userMention matches the opening tag of a user mention, as in
	// [USER=9001]Loa.Alpha[/USER], and captures the member's user id.
	userMention = regexp.MustCompile(`(?i)\[user=(\d+)\]`)

	// usernameLabel allows the stray "$" form 12 writes before the value.
	usernameLabel  = regexp.MustCompile(`(?i)^\s*username\b\s*:?\s*\$?\s*(.*?)\s*$`)
	startDateLabel = regexp.MustCompile(`(?i)^\s*start date\s*:?\s*(.*?)\s*$`)
	endDateLabel   = regexp.MustCompile(`(?i)^\s*end date\s*:?\s*(.*?)\s*$`)
	// reasonLabel opens the reason field. The reservist form labels it
	// "Details", and the other LOA forms label it "Reason:".
	reasonLabel = regexp.MustCompile(`(?i)^\s*(?:reason|details)\b\s*:?\s*(.*?)\s*$`)
	// boldLabelLine matches a line that opens with a bold label. Every form
	// question starts with one, and no stored reason line does.
	boldLabelLine = regexp.MustCompile(`(?i)^\s*\[b\]`)
)

// loaDateLayouts are the layouts the LOA forms' date question writes, plus
// the full-month spelling hand posts use. time.Parse matches month names
// without regard to case, and "2" takes one or two digits.
var loaDateLayouts = []string{"Jan 2, 2006", "January 2, 2006"}

// parseLoaPost reads an LOA from the opening post of its thread as the post
// reads now, edits included. It never reads the LOA form's stored answers.
// Those freeze at submission, but leaders extend about 40 LOAs a year by
// editing the post, so the answers would keep the old End Date. A hand-posted
// LOA has no submission at all. Moving this reader to the forms tables would
// lose both. FindLoas reads a submission's answers only for a date or reason
// the post doesn't give.
//
// The forms write each answer under a bold, colored question label. Hand
// copies drop the markup or lowercase it, and line endings mix "\r\n" and
// "\n". With the BBCode stripped, every layout reads as the label, an
// optional colon, and the value. Username's value sits on the label's line. A
// date's value sits on the label's line or the next non-blank one. The
// reason's value can run over several lines.
func parseLoaPost(message string) loaPost {
	var p loaPost
	lines, plain := textLines(message)
	p.reason = loaReason(lines, plain)
	var seenStart, seenEnd bool
	for i, line := range lines {
		// The last Username label names the Subject. On an LOA filed for
		// someone else, an earlier label names the Submitter.
		if m := usernameLabel.FindStringSubmatch(plain[i]); m != nil {
			p.subject, p.hasSubject = m[1], true
			p.mentionID = mentionedUser(line)
			continue
		}
		dateLine := dateText(line)
		if m := startDateLabel.FindStringSubmatch(dateLine); m != nil && !seenStart {
			seenStart = true
			p.start = parseLoaDate(labelValue(m[1], lines[i+1:]))
			continue
		}
		if m := endDateLabel.FindStringSubmatch(dateLine); m != nil && !seenEnd {
			seenEnd = true
			p.end = parseLoaDate(labelValue(m[1], lines[i+1:]))
		}
	}
	return p
}

// mentionedUser returns the user id a line's user mention names, or 0 when
// the line holds no mention.
func mentionedUser(line string) uint64 {
	m := userMention.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	id, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// loaReason reads the reason field as plain text, or returns "" when the post
// has no reason label. lines and plain are the post's lines as textLines
// returns them. The value is the rest of the label's line and every line
// after it, up to the next bold label or the end of the post. Each line loses
// its BBCode and stays a line of its own, so a consumer can show the reason
// as the trooper wrote it without rendering BBCode.
func loaReason(lines, plain []string) string {
	for i, text := range plain {
		m := reasonLabel.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		end := i + 1
		for end < len(lines) && !boldLabelLine.MatchString(lines[end]) {
			end++
		}
		return joinLines(append([]string{m[1]}, plain[i+1:end]...))
	}
	return ""
}

// plainText is s with its BBCode stripped and its line breaks kept. A form
// answer's reason takes the same shape as the post's.
func plainText(s string) string {
	_, plain := textLines(s)
	return joinLines(plain)
}

// textLines splits a post or form answer into lines, and returns each line a
// second time with its BBCode stripped. The forum ends lines with "\r\n" or
// "\n", and both split the same way.
func textLines(s string) (lines, plain []string) {
	lines = strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	plain = make([]string, len(lines))
	for i, line := range lines {
		plain[i] = stripBBCode(line)
	}
	return lines, plain
}

// joinLines joins plain lines into one text with "\n" between them, and
// trims the blank space at either end.
func joinLines(lines []string) string {
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// dateText reads one line of a date field. It drops struck-through text
// before the rest of the markup. A leader who extends an LOA strikes the old
// date and writes the new one after it, as in
// "[S]Oct 9, 2026[/S] -> Oct 16, 2026", and the new date wins.
func dateText(line string) string {
	return stripBBCode(struckText.ReplaceAllString(line, ""))
}

func stripBBCode(s string) string {
	return bbcodeTag.ReplaceAllString(s, "")
}

// labelValue returns a date label's value. That is the rest of the label's
// own line when it has one, and otherwise the next non-blank line.
func labelValue(sameLine string, rest []string) string {
	if strings.TrimSpace(sameLine) != "" {
		return sameLine
	}
	for _, line := range rest {
		if v := dateText(line); strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parseLoaDate reads a date in one of loaDateLayouts as a UTC calendar day,
// or returns the zero time. It skips leading punctuation, such as the arrow
// an edit leaves behind once the struck date is gone.
func parseLoaDate(s string) time.Time {
	s = strings.TrimSpace(strings.TrimLeftFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}))
	for _, layout := range loaDateLayouts {
		if d, err := time.Parse(layout, s); err == nil {
			return d
		}
	}
	return time.Time{}
}

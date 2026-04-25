package main

import (
	"crypto/rand"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

func generateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "Just now"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "Just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", mins)
	}
	if d < 24*time.Hour {
		hrs := int(d.Hours())
		if hrs == 1 {
			return "1 hr ago"
		}
		return fmt.Sprintf("%d hrs ago", hrs)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	if days < 30 {
		return fmt.Sprintf("%d days ago", days)
	}
	return t.Format("02 Jan 2006")
}

func copyToClipboard(text string) {
	if _, err := exec.LookPath("pbcopy"); err == nil {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	} else if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command("wl-copy")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	} else if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	}
}

func sanitizeFilename(name string) string {
	re := regexp.MustCompile(`[\\/:*?"<>|]`)
	return re.ReplaceAllString(name, "")
}

func getFirstHeading(content string) string {
	lines := strings.Split(content, "\n")
	inCodeBlock := false
	for _, line := range lines {
		if strings.Count(line, "```")%2 != 0 {
			inCodeBlock = !inCodeBlock
		}
		if inCodeBlock || strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimPrefix(trimmed, "# ")
		} else if strings.HasPrefix(trimmed, "## ") {
			return strings.TrimPrefix(trimmed, "## ")
		} else if strings.HasPrefix(trimmed, "### ") {
			return strings.TrimPrefix(trimmed, "### ")
		}
	}
	return "Untitled"
}

func extractTitle(content string) string {
	title := getFirstHeading(content)
	if title != "Untitled" && title != "" {
		return sanitizeFilename(title)
	}
	return "note"
}

func gatherAllTags(notes []Note) []string {
	tagCounts := make(map[string]int)
	for _, n := range notes {
		for _, tag := range n.Tags {
			tagCounts[tag]++
		}
	}

	var uniqueTags []string
	for tag, count := range tagCounts {
		uniqueTags = append(uniqueTags, fmt.Sprintf("%s (%d)", tag, count))
	}

	sort.Strings(uniqueTags)
	return uniqueTags
}
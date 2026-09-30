package webui

import "strings"

var ghostCharReplacer = strings.NewReplacer(
	"\u200b", "", "\u200c", "", "\u200d", "",
	"\u200e", "", "\u200f", "", "\ufeff", "", "\u00a0", " ",
)

var browserTitleSuffixes = []string{
	"google chrome", "microsoft edge", "msedge",
	"brave", "vivaldi", "firefox", "opera", "chromium",
}

func isStandardBrowserWindow(title string) bool {
	clean := ghostCharReplacer.Replace(strings.ToLower(title))
	clean = strings.TrimSpace(clean)
	for _, b := range browserTitleSuffixes {
		if strings.HasSuffix(clean, b) {
			return true
		}
	}
	return false
}

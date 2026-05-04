package reels

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	ErrNoURL          = errors.New("no URL found")
	ErrUnsupportedURL = errors.New("unsupported URL")
	ErrMultipleURLs   = errors.New("multiple URLs found")

	urlPattern = regexp.MustCompile(`(?i)(?:https?://)?(?:[a-z0-9-]+\.)+[a-z]{2,}(?:/[^\s<>"']*)?`)
)

func ExtractURL(text string) (string, error) {
	matches := urlPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return "", ErrNoURL
	}
	if len(matches) > 1 {
		return "", ErrMultipleURLs
	}

	rawURL := trimURL(matches[0])
	parsedURL, err := parseURL(rawURL)
	if err != nil {
		return "", ErrUnsupportedURL
	}
	if !isSupportedURL(parsedURL) {
		return "", ErrUnsupportedURL
	}

	return rawURL, nil
}

func parseURL(rawURL string) (*url.URL, error) {
	value := rawURL
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	return url.Parse(value)
}

func isSupportedURL(value *url.URL) bool {
	host := strings.ToLower(value.Hostname())
	path := value.EscapedPath()

	switch host {
	case "tiktok.com", "www.tiktok.com", "vm.tiktok.com", "vt.tiktok.com":
		return true
	case "instagram.com", "www.instagram.com":
		return hasAnyPathPrefix(path, "/reel/", "/reels/", "/p/")
	default:
		return false
	}
}

func hasAnyPathPrefix(path string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func trimURL(value string) string {
	return strings.TrimRight(value, ".,!?;:)]}")
}

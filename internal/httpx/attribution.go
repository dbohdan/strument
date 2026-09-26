package httpx

import "net/http"

// AppName and AppURL identify Strument to services that attribute requests to
// an app. OpenRouter lists requests by these, and shows one without them as
// "Unknown".
const (
	AppName = "Strument"
	AppURL  = "https://dbohdan.com/strument"
)

// SetAppAttribution adds OpenRouter's app-attribution headers. Its docs write
// "HTTP-Referer"; Go canonicalizes that to "Http-Referer", and header names are
// case-insensitive (RFC 9110), so it matches. They say only which app sent the
// request, which the User-Agent already says, so sending them to an endpoint
// that ignores them costs nothing.
func SetAppAttribution(h http.Header) {
	h.Set("Http-Referer", AppURL)
	h.Set("X-Title", AppName)
}

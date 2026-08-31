package httpapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/httpapi"
)

func TestParsePublicBaseURL(t *testing.T) {
	valid := []string{
		"https://app.tomte.test",
		"https://app.tomte.test:8443",
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	}
	for _, in := range valid {
		u, err := httpapi.ParsePublicBaseURL(in)
		require.NoError(t, err, in)
		require.Equal(t, in, u.String())
	}

	invalid := []string{
		"",
		"app.tomte.test",             // no scheme
		"http://app.tomte.test",      // http off localhost: carries tokens, defines Origin
		"https://app.tomte.test/",    // trailing slash
		"https://app.tomte.test/app", // path
		"https://app.tomte.test?x=1", // query
		"https://user@app.tomte.test",
		"ftp://app.tomte.test",
	}
	for _, in := range invalid {
		_, err := httpapi.ParsePublicBaseURL(in)
		require.Error(t, err, in)
	}
}

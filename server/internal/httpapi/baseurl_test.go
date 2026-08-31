package httpapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/httpapi"
)

func TestParsePublicBaseURL(t *testing.T) {
	valid := []string{
		"https://app.nightshift.test",
		"https://app.nightshift.test:8443",
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
		"app.nightshift.test",             // no scheme
		"http://app.nightshift.test",      // http off localhost: carries tokens, defines Origin
		"https://app.nightshift.test/",    // trailing slash
		"https://app.nightshift.test/app", // path
		"https://app.nightshift.test?x=1", // query
		"https://user@app.nightshift.test",
		"ftp://app.nightshift.test",
	}
	for _, in := range invalid {
		_, err := httpapi.ParsePublicBaseURL(in)
		require.Error(t, err, in)
	}
}

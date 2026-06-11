package parquet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.k6.io/k6/v2/lib"
	"gopkg.in/guregu/null.v3"
)

// TestSafeScriptOptions_RedactsTLSAuth is a regression test for a credential
// leak: lib.Options.TLSAuth carries the operator's PEM-encoded client
// certificate, private key, and decryption password, and the previous
// implementation embedded the full lib.Options JSON into the file footer.
func TestSafeScriptOptions_RedactsTLSAuth(t *testing.T) {
	t.Parallel()

	opts := lib.Options{
		VUs: null.IntFrom(5),
		TLSAuth: []*lib.TLSAuth{{
			TLSAuthFields: lib.TLSAuthFields{
				Cert:     "-----BEGIN CERTIFICATE-----\nDO_NOT_LEAK_CERT\n-----END CERTIFICATE-----",
				Key:      "-----BEGIN PRIVATE KEY-----\nDO_NOT_LEAK_KEY\n-----END PRIVATE KEY-----",
				Password: null.StringFrom("hunter2"),
				Domains:  []string{"example.com"},
			},
		}},
	}

	out, err := safeScriptOptions(opts)
	require.NoError(t, err)

	for _, needle := range []string{"DO_NOT_LEAK_CERT", "DO_NOT_LEAK_KEY", "hunter2", "BEGIN PRIVATE KEY", "tlsAuth"} {
		assert.NotContains(t, out, needle, "footer must not contain %q", needle)
	}

	// Non-secret allowlisted fields still round-trip.
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Contains(t, got, "vus")
}

func TestSafeScriptOptions_DropsCloudAndExt(t *testing.T) {
	t.Parallel()

	opts := lib.Options{
		Cloud:    json.RawMessage(`{"token":"DO_NOT_LEAK_TOKEN"}`),
		External: map[string]json.RawMessage{"third_party": json.RawMessage(`{"apiKey":"DO_NOT_LEAK_APIKEY"}`)},
	}

	out, err := safeScriptOptions(opts)
	require.NoError(t, err)

	for _, needle := range []string{"DO_NOT_LEAK_TOKEN", "DO_NOT_LEAK_APIKEY", "cloud", "ext"} {
		assert.NotContains(t, out, needle, "footer must not contain %q", needle)
	}
}

// TestNewRunMetadata_FooterCarriesNoSecrets exercises the same redaction
// through the public newRunMetadata → KeyValueMetadata path the writer uses.
func TestNewRunMetadata_FooterCarriesNoSecrets(t *testing.T) {
	t.Parallel()

	opts := lib.Options{
		TLSAuth: []*lib.TLSAuth{{
			TLSAuthFields: lib.TLSAuthFields{Key: "-----BEGIN PRIVATE KEY-----\nDO_NOT_LEAK\n-----END PRIVATE KEY-----"},
		}},
	}
	cfg := NewConfig()
	cfg.Compression = null.StringFrom("zstd")
	cfg.RowGroupSize = null.IntFrom(100)

	m := newRunMetadata("run-id", "", "", "", opts, cfg)
	for k, v := range m.KeyValueMetadata() {
		assert.False(t, strings.Contains(v, "DO_NOT_LEAK"), "%s must not leak", k)
	}
}

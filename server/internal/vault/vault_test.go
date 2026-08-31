package vault_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/vault"
)

func testMaster(t *testing.T) *vault.Master {
	t.Helper()
	key := make([]byte, vault.KeyLen)
	_, err := rand.Read(key)
	require.NoError(t, err)
	m, err := vault.NewMaster(key)
	require.NoError(t, err)
	return m
}

func TestSecretRoundTrip(t *testing.T) {
	m := testMaster(t)
	kek, err := m.NewTenantKEK()
	require.NoError(t, err)

	dek, ct, nonce, err := m.EncryptSecret(kek, "sk-ant-secret")
	require.NoError(t, err)
	got, err := m.DecryptSecret(kek, dek, ct, nonce)
	require.NoError(t, err)
	require.Equal(t, "sk-ant-secret", got)
}

func TestTenantIsolationAndWrongMaster(t *testing.T) {
	m := testMaster(t)
	kekA, err := m.NewTenantKEK()
	require.NoError(t, err)
	kekB, err := m.NewTenantKEK()
	require.NoError(t, err)
	require.False(t, bytes.Equal(kekA, kekB))

	dek, ct, nonce, err := m.EncryptSecret(kekA, "tenant-a-secret")
	require.NoError(t, err)

	// Another tenant's KEK cannot open it.
	_, err = m.DecryptSecret(kekB, dek, ct, nonce)
	require.Error(t, err)

	// A different master cannot even unwrap the KEK.
	other := testMaster(t)
	_, err = other.DecryptSecret(kekA, dek, ct, nonce)
	require.Error(t, err)
}

func TestNewMasterRejectsBadKey(t *testing.T) {
	_, err := vault.NewMaster([]byte("short"))
	require.Error(t, err)
}

func TestDecryptSecretRejectsTruncatedNonce(t *testing.T) {
	m := testMaster(t)
	kek, err := m.NewTenantKEK()
	require.NoError(t, err)

	dek, ct, nonce, err := m.EncryptSecret(kek, "sk-ant-secret")
	require.NoError(t, err)

	_, err = m.DecryptSecret(kek, dek, ct, nonce[:len(nonce)-1])
	require.Error(t, err)
}

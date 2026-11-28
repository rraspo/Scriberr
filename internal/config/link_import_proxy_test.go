package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadReadsTheLinkImportProxy(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-for-config")
	t.Setenv("LINK_IMPORT_PROXY", " http://egress-proxy.example:3128 ")
	require.Equal(t, "http://egress-proxy.example:3128", Load().LinkImportProxy)

	t.Setenv("LINK_IMPORT_PROXY", "")
	require.Empty(t, Load().LinkImportProxy)
}

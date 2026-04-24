package secret

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_NoOpOnPlainString(t *testing.T) {
	out, err := Resolve("plain value")
	require.NoError(t, err)
	assert.Equal(t, "plain value", out)
}

func TestResolve_EnvSubstitution(t *testing.T) {
	t.Setenv("MY_VAR", "hello")
	out, err := Resolve("prefix-${env:MY_VAR}-suffix")
	require.NoError(t, err)
	assert.Equal(t, "prefix-hello-suffix", out)
}

func TestResolve_MultipleSubstitutions(t *testing.T) {
	t.Setenv("A", "ALPHA")
	t.Setenv("B", "BETA")
	out, err := Resolve("${env:A}/${env:B}")
	require.NoError(t, err)
	assert.Equal(t, "ALPHA/BETA", out)
}

func TestResolve_MissingEnvErrors(t *testing.T) {
	os.Unsetenv("DEFINITELY_NOT_SET_ENV_VAR_12345")
	_, err := Resolve("${env:DEFINITELY_NOT_SET_ENV_VAR_12345}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEFINITELY_NOT_SET_ENV_VAR_12345")
}

func TestResolve_UnknownSchemeErrors(t *testing.T) {
	_, err := Resolve("${nope:x}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

func TestResolve_DollarDollarEscapeIsLiteralDollar(t *testing.T) {
	out, err := Resolve("price is $$5")
	require.NoError(t, err)
	assert.Equal(t, "price is $5", out)
}

func TestResolve_StrayDollarErrors(t *testing.T) {
	_, err := Resolve("a $ b")
	require.Error(t, err)
}

func TestResolve_UnterminatedPlaceholderErrors(t *testing.T) {
	_, err := Resolve("${env:FOO")
	require.Error(t, err)
}

func TestResolve_MalformedPlaceholderErrors(t *testing.T) {
	_, err := Resolve("${noScheme}")
	require.Error(t, err)
}

func TestResolveEnv_AppliesToAllValues(t *testing.T) {
	t.Setenv("TOKEN", "tok")
	in := map[string]string{
		"GITHUB_TOKEN": "${env:TOKEN}",
		"PLAIN":        "verbatim",
	}
	out, err := ResolveEnv(in)
	require.NoError(t, err)
	assert.Equal(t, "tok", out["GITHUB_TOKEN"])
	assert.Equal(t, "verbatim", out["PLAIN"])
}

func TestRefs_FindsAllEnvReferences(t *testing.T) {
	got := Refs("hello ${env:A} world ${env:B} ${env:A}")
	assert.ElementsMatch(t, []string{"A", "B"}, got)
}

func TestRefs_IgnoresUnknownSchemes(t *testing.T) {
	got := Refs("${secret:X} ${env:Y}")
	assert.Equal(t, []string{"Y"}, got)
}

func TestRefs_EmptyOnPlainString(t *testing.T) {
	assert.Empty(t, Refs("nothing here"))
}

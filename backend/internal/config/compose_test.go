package config

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The api and scheduler containers get an explicit environment list (the
// x-backend-env anchor in docker-compose.yml) instead of the whole .env, so
// they never see DB_ROOT_PASSWORD. This test keeps that list in sync with
// envBindings: a setting missing there would silently fall back to its
// default in the self-hosted stack.
func TestCompose_BackendEnvMatchesConfig(t *testing.T) {
	raw, err := os.ReadFile("../../../docker-compose.yml")
	require.NoError(t, err)
	compose := string(raw)

	anchor := regexp.MustCompile(`(?s)x-backend-env: &backend-env\n(.*?)\n\n`).FindStringSubmatch(compose)
	require.Len(t, anchor, 2, "docker-compose.yml must define x-backend-env: &backend-env")
	block := anchor[1]

	// Set per service to the compose hosts, not from .env.
	perService := map[string]bool{"DB_HOST": true, "DB_PORT": true, "REDIS_HOST": true, "REDIS_PORT": true}
	for _, env := range envBindings {
		if perService[env] {
			continue
		}
		assert.Contains(t, block, "  "+env+": ", "%s is read by config.go but not passed to api/scheduler", env)
	}
	assert.NotContains(t, block, "DB_ROOT_PASSWORD")
	assert.NotContains(t, compose, "env_file:", "env_file would hand every .env secret to the containers")
	assert.Equal(t, 2, strings.Count(compose, "<<: *backend-env"), "api and scheduler both use the anchor")
}

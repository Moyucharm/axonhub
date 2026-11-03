package gql

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
)

// Validate the actual frontend selections, including the shared mutation fragment,
// so merging an upstream fragment cannot silently drop the self-hosted fields.
func TestChannelFrontendOperationsAfterReleaseMerge(t *testing.T) {
	data, err := os.ReadFile("../../../frontend/src/features/channels/data/channels.ts")
	require.NoError(t, err)
	constants := map[string]string{}
	pattern := regexp.MustCompile("(?s)const ([A-Z][A-Z_]+) = `([^`]+)`;")
	for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
		constants[match[1]] = match[2]
	}
	schema := NewExecutableSchema(Config{}).Schema()
	for name, query := range constants {
		if !strings.Contains(query, "query ") && !strings.Contains(query, "mutation ") {
			continue
		}
		for variable, value := range constants {
			query = strings.ReplaceAll(query, "${"+variable+"}", value)
		}
		if strings.Contains(query, "${") {
			continue // Runtime-built queries are covered by their data-layer tests.
		}
		t.Run(name, func(t *testing.T) {
			_, errors := gqlparser.LoadQuery(schema, query)
			require.Empty(t, errors)
		})
	}

	fragment := constants["CHANNEL_FIELDS_FRAGMENT"]
	for _, field := range []string{"apiKeyPool", "codexSimulation", "apiKeyStates", "cooldownUntil", "rateLimit", "vertexApiKeys", "maxSingleChannelRetries", "responseTimeout"} {
		require.Contains(t, fragment, field)
	}

	patch := schema.Types["UpdateChannelSettingsInput"]
	for _, field := range []string{"apiKeyPool", "codexSimulation", "maxSingleChannelRetries", "responseTimeout"} {
		require.NotNil(t, patch.Fields.ForName(field), "settings patch must retain %s", field)
	}
}

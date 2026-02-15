package via

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptions_ZeroValues(t *testing.T) {
	opts := Options{}

	assert.False(t, opts.DevMode)
	assert.Equal(t, "", opts.ServerAddress)
	assert.Equal(t, LogLevel(0), opts.LogLvl)
	assert.Equal(t, "", opts.DocumentTitle)
	assert.Equal(t, 0, opts.SessionTTL)
}

func TestOptions_CustomValues(t *testing.T) {
	opts := Options{
		DevMode:       true,
		ServerAddress: ":8080",
		LogLvl:        LogLevelDebug,
		DocumentTitle: "My App",
		SessionTTL:    3600,
	}

	assert.True(t, opts.DevMode)
	assert.Equal(t, ":8080", opts.ServerAddress)
	assert.Equal(t, LogLevelDebug, opts.LogLvl)
	assert.Equal(t, "My App", opts.DocumentTitle)
	assert.Equal(t, 3600, opts.SessionTTL)
}

func TestV_DefaultConfig(t *testing.T) {
	v := New()

	assert.False(t, v.cfg.DevMode)
	assert.Equal(t, ":3000", v.cfg.ServerAddress)
	assert.Equal(t, LogLevelInfo, v.cfg.LogLvl)
	assert.Equal(t, "⚡ Via", v.cfg.DocumentTitle)
	assert.Equal(t, 1800, v.cfg.SessionTTL)
}

func TestLogLevel_Order(t *testing.T) {
	assert.Equal(t, LogLevel(0), undefined)
	assert.Equal(t, LogLevel(1), LogLevelError)
	assert.Equal(t, LogLevel(2), LogLevelWarn)
	assert.Equal(t, LogLevel(3), LogLevelInfo)
	assert.Equal(t, LogLevel(4), LogLevelDebug)
}

func TestPlugin_Type(t *testing.T) {
	// Create a test plugin that implements the Plugin interface
	testPlugin := &testPluginImpl{}

	var p Plugin = testPlugin
	assert.NotNil(t, p)
}

// testPluginImpl is a test implementation of the Plugin interface
type testPluginImpl struct{}

func (t *testPluginImpl) Register(v *V) {}

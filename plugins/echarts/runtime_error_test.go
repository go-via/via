package echarts_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type badChartPage struct {
	Chart *echarts.Chart
}

func (p *badChartPage) OnInit(ctx *via.Ctx) error {
	if p.Chart == nil {
		p.Chart = echarts.NewChart(echarts.WithElementID("bad"))
	}
	return nil
}

func (p *badChartPage) BadSetOption(ctx *via.Ctx) error {
	return p.Chart.SetOption(ctx, map[string]any{"x": make(chan int)})
}

func (p *badChartPage) BadSetSeries(ctx *via.Ctx) error {
	return p.Chart.SetSeries(ctx, map[string]any{"data": make(chan int)})
}

func (p *badChartPage) BadAppendData(ctx *via.Ctx) error {
	return p.Chart.AppendData(ctx, 0, [][]any{{make(chan int)}})
}

func (p *badChartPage) View(ctx *via.CtxR) h.H {
	if p.Chart == nil {
		return h.Div()
	}
	return p.Chart.Mount()
}

func fireBadChartAction(t *testing.T, action string) (<-chan string, error) {
	t.Helper()
	var server *httptest.Server
	errs := make(chan error, 1)
	app := via.New(
		via.WithTestServer(&server),
		via.WithActionErrorHandler(func(_ *via.Ctx, err error) {
			select {
			case errs <- err:
			default:
			}
		}),
	)
	via.Mount[badChartPage](app, "/")
	t.Cleanup(server.Close)

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	t.Cleanup(cancel)

	require.Equal(t, 200, tc.Action(action).Fire())

	select {
	case err := <-errs:
		return frames, err
	case <-time.After(2 * time.Second):
		t.Fatalf("expected action error from %s", action)
		return frames, nil
	}
}

func TestChartAPI_SetOption_surfacesMarshalFailureAsError(t *testing.T) {
	t.Parallel()
	frames, err := fireBadChartAction(t, "BadSetOption")
	require.Error(t, err, "SetOption must return an error when opts are unmarshalable")

	select {
	case f := <-frames:
		assert.NotContains(t, f, "setOption",
			"no setOption script should be emitted when marshalling failed")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestChartAPI_SetSeries_surfacesMarshalFailureAsError(t *testing.T) {
	t.Parallel()
	frames, err := fireBadChartAction(t, "BadSetSeries")
	require.Error(t, err, "SetSeries must return an error when series are unmarshalable")

	select {
	case f := <-frames:
		assert.NotContains(t, f, "setOption",
			"no setOption script should be emitted when marshalling failed")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestChartAPI_AppendData_surfacesMarshalFailureAsError(t *testing.T) {
	t.Parallel()
	frames, err := fireBadChartAction(t, "BadAppendData")
	require.Error(t, err, "AppendData must return an error when data is unmarshalable")

	select {
	case f := <-frames:
		assert.NotContains(t, f, "appendData",
			"no appendData script should be emitted when marshalling failed")
	case <-time.After(150 * time.Millisecond):
	}
}

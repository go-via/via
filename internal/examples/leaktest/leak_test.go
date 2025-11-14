package main

import (
	"fmt"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

func TestLeaks(t *testing.T) {
	// Headless runs the browser on foreground, you can also use flag "-rod=show"
	// Devtools opens the tab in each new tab opened automatically
	l := launcher.New().
		Headless(false).
		Devtools(true)

	defer l.Cleanup()
	url := l.MustLaunch()

	// Trace shows verbose debug information for each action executed
	// SlowMotion is a debug related function that waits 2 seconds between
	// each action, making it easier to inspect what your code is doing.
	browser := rod.New().
		ControlURL(url).
		// Trace(true).
		// SlowMotion(2 * time.Second).
		MustConnect()
	page := browser.MustPage("http://localhost:3000")

	for x := range 50 {
		fmt.Println(x)
		// Handle the beforeunload dialog that blocks reload
		wait, handle := page.MustHandleDialog()
		page.MustElement("body").MustClick() // only focused page will handle beforeunload event
		go page.Reload()                     // trigger reload in goroutine
		wait()                               // wait for dialog on main thread
		handle(true, "")                     // accept the dialog
		page.MustWaitLoad()                  // wait for page to finish loading
	}

	defer browser.MustClose() //
}

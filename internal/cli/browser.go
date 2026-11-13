package cli

import (
	"io"
	"sync"

	osbrowser "github.com/cli/browser"
	"github.com/cli/go-gh/v2/pkg/browser"
)

var quietBrowserOnce sync.Once

// Open is used only by the TUI. go-gh's configured launcher accepts writers;
// its OS fallback uses cli/browser's global writers instead. Silence both.
func quietBrowser(url string) error {
	quietBrowserOnce.Do(func() { osbrowser.Stdout = io.Discard; osbrowser.Stderr = io.Discard })
	return browser.New("", io.Discard, io.Discard).Browse(url)
}

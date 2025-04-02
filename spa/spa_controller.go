package spa

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/go-raptor/components"
)

type SPAController struct {
	components.Controller

	lock  sync.RWMutex
	files map[string]bool
}

func NewSPAController() *SPAController {
	return &SPAController{
		files: make(map[string]bool),
	}
}

func (sc *SPAController) Index(c *components.Context) error {
	requestedPath := c.Request().URL.Path
	filePath := filepath.Join("public", requestedPath)

	sc.lock.RLock()
	exists, inCache := sc.files[filePath]
	sc.lock.RUnlock()

	if inCache {
		if exists {
			return c.File(filePath)
		}
		return c.File("public/index.html")
	}

	fileInfo, err := os.Stat(filePath)
	if err == nil && !fileInfo.IsDir() {
		sc.lock.Lock()
		sc.files[filePath] = true
		sc.lock.Unlock()
		return c.File(filePath)
	}

	sc.lock.Lock()
	sc.files[filePath] = false
	sc.lock.Unlock()
	return c.File("public/index.html")
}

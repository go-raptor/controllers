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

	directory string
	file      string
}

func NewSPAController(directory, file string) *SPAController {
	return &SPAController{
		files:     make(map[string]bool),
		directory: directory,
		file:      directory + "/" + file,
	}
}

func (sc *SPAController) Index(s components.State) error {
	requestedPath := s.Request().URL.Path
	filePath := filepath.Join(sc.directory, requestedPath)

	sc.lock.RLock()
	exists, inCache := sc.files[filePath]
	sc.lock.RUnlock()

	if inCache {
		if exists {
			return s.File(filePath)
		}
		return s.File(sc.file)
	}

	fileInfo, err := os.Stat(filePath)
	if err == nil && !fileInfo.IsDir() {
		sc.lock.Lock()
		sc.files[filePath] = true
		sc.lock.Unlock()
		return s.File(filePath)
	}

	sc.lock.Lock()
	sc.files[filePath] = false
	sc.lock.Unlock()
	return s.File(sc.file)
}

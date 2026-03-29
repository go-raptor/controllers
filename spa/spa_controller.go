package spa

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/go-raptor/raptor/v4"
)

type SPAController struct {
	raptor.Controller

	directory string
	indexFile string
}

func NewSPAController(directory, file string) *SPAController {
	absDir, _ := filepath.Abs(directory)
	return &SPAController{
		directory: absDir,
		indexFile: filepath.Join(absDir, file),
	}
}

func (sc *SPAController) Index(c *raptor.Context) error {
	filePath := filepath.Join(sc.directory, filepath.Clean("/"+c.Request().URL.Path))

	if !strings.HasPrefix(filePath, sc.directory) {
		return c.File(sc.indexFile)
	}

	info, err := os.Stat(filePath)
	if err == nil && !info.IsDir() {
		return c.File(filePath)
	}

	return c.File(sc.indexFile)
}

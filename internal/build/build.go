package build

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/client"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/ui"
)

// buildMessage rappresenta un singolo messaggio JSON dallo streaming di build Docker.
type buildMessage struct {
	Stream string `json:"stream"`
	Error  string `json:"error"`
}

// BuildImage builda l'immagine Docker con output in streaming (D-12).
// Crea un tar context rispettando .dockerignore e streamma l'output JSON linea per linea.
func BuildImage(ctx context.Context, cli client.APIClient, cfg *config.Config) error {
	ui.Info("Building image " + cfg.ImageRef() + "...")

	buildCtx, err := createTarContext(cfg.Build.Context)
	if err != nil {
		return fmt.Errorf("creating build context: %w", err)
	}

	resp, err := cli.ImageBuild(ctx, buildCtx, build.ImageBuildOptions{
		Dockerfile: cfg.Build.Dockerfile,
		Tags:       []string{cfg.ImageRef()},
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("starting image build: %w", err)
	}
	defer resp.Body.Close()

	if err := streamBuildOutput(resp.Body); err != nil {
		return err
	}

	ui.Success("Image " + cfg.ImageRef() + " built successfully")
	return nil
}

// streamBuildOutput legge lo streaming JSON di Docker build e stampa l'output in tempo reale.
func streamBuildOutput(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var msg buildMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			// Ignora linee non-JSON (possibili keep-alive)
			continue
		}
		if msg.Error != "" {
			return fmt.Errorf("build error: %s", msg.Error)
		}
		if msg.Stream != "" {
			fmt.Print(msg.Stream)
		}
	}
	return scanner.Err()
}

// createTarContext crea un archivio tar del build context, rispettando .dockerignore (T-02-07).
func createTarContext(contextDir string) (io.Reader, error) {
	ignorePatterns := readDockerignore(contextDir)

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := filepath.Walk(contextDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Calcola il path relativo al context
			relPath, err := filepath.Rel(contextDir, path)
			if err != nil {
				return err
			}

			// Salta la root stessa
			if relPath == "." {
				return nil
			}

			// Controlla se il path matcha un pattern di ignore
			if shouldIgnore(relPath, info.IsDir(), ignorePatterns) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Crea header tar
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = relPath

			// Per symlink, leggi il target
			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(path)
				if err != nil {
					return err
				}
				header.Linkname = link
			}

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			// Scrivi contenuto solo per file regolari
			if !info.Mode().IsRegular() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(tw, f)
			return err
		})

		tw.Close()
		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()

	return pr, nil
}

// shouldIgnore verifica se un path deve essere ignorato in base ai pattern .dockerignore.
func shouldIgnore(relPath string, isDir bool, patterns []string) bool {
	for _, pattern := range patterns {
		// Pattern directory (terminano con /)
		dirPattern := strings.TrimSuffix(pattern, "/")
		if dirPattern != pattern {
			// Era un pattern directory
			if isDir && (relPath == dirPattern || strings.HasPrefix(relPath, dirPattern+"/")) {
				return true
			}
			if !isDir && strings.HasPrefix(relPath, dirPattern+"/") {
				return true
			}
			continue
		}

		// Pattern glob (es. *.md)
		if matched, _ := filepath.Match(pattern, filepath.Base(relPath)); matched {
			return true
		}

		// Match esatto
		if relPath == pattern {
			return true
		}
	}
	return false
}

// readDockerignore legge e parsa il file .dockerignore dal build context.
func readDockerignore(contextDir string) []string {
	path := filepath.Join(contextDir, ".dockerignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

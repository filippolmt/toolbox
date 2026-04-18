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

// buildMessage is a single JSON message from the Docker build output stream.
type buildMessage struct {
	Stream string `json:"stream"`
	Error  string `json:"error"`
}

// BuildImage builds the Docker image with streaming output (D-12).
// Creates a tar context honoring .dockerignore and streams JSON output line by line.
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

// streamBuildOutput reads Docker's JSON build stream and prints output in real time.
func streamBuildOutput(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var msg buildMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			// Ignore non-JSON lines (possible keep-alives).
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

// createTarContext builds a tar archive of the build context honoring .dockerignore (T-02-07).
func createTarContext(contextDir string) (io.Reader, error) {
	ignorePatterns := readDockerignore(contextDir)

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := filepath.Walk(contextDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Path relative to the context root.
			relPath, err := filepath.Rel(contextDir, path)
			if err != nil {
				return err
			}

			// Skip the root itself.
			if relPath == "." {
				return nil
			}

			// Honor ignore patterns.
			if shouldIgnore(relPath, info.IsDir(), ignorePatterns) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Build the tar header.
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = relPath

			// For symlinks, record the target.
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

			// Write content only for regular files.
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

// shouldIgnore reports whether a path matches one of the .dockerignore patterns.
func shouldIgnore(relPath string, isDir bool, patterns []string) bool {
	for _, pattern := range patterns {
		// Directory patterns (ending with /).
		dirPattern := strings.TrimSuffix(pattern, "/")
		if dirPattern != pattern {
			// Was a directory pattern.
			if isDir && (relPath == dirPattern || strings.HasPrefix(relPath, dirPattern+"/")) {
				return true
			}
			if !isDir && strings.HasPrefix(relPath, dirPattern+"/") {
				return true
			}
			continue
		}

		// Glob pattern (e.g. *.md).
		if matched, _ := filepath.Match(pattern, filepath.Base(relPath)); matched {
			return true
		}

		// Exact match.
		if relPath == pattern {
			return true
		}
	}
	return false
}

// readDockerignore parses .dockerignore from the build context, returning its patterns.
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

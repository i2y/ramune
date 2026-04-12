package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var registryClient = &http.Client{Timeout: 30 * time.Second}

const npmRegistryURL = "https://registry.npmjs.org"

type registryMetadata struct {
	Name     string                         `json:"name"`
	DistTags map[string]string              `json:"dist-tags"`
	Versions map[string]registryVersionInfo `json:"versions"`
}

type registryVersionInfo struct {
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	OS                   []string          `json:"os"`
	CPU                  []string          `json:"cpu"`
	Dist                 registryDist      `json:"dist"`
}

type registryDist struct {
	Tarball   string `json:"tarball"`
	Integrity string `json:"integrity"`
	Shasum    string `json:"shasum"`
}

// ResolvedPackage is the result of resolving a package name + version range.
type ResolvedPackage struct {
	Name                 string
	Version              string
	Integrity            string
	Tarball              string
	Dependencies         map[string]string
	OptionalDependencies map[string]string
	Optional             bool // true if this was resolved as an optional dependency
}

func fetchRegistryMetadata(name string) (*registryMetadata, error) {
	encodedName := name
	if strings.HasPrefix(name, "@") {
		encodedName = "@" + url.PathEscape(name[1:])
	}

	resp, err := registryClient.Get(npmRegistryURL + "/" + encodedName)
	if err != nil {
		return nil, fmt.Errorf("ramune: registry request failed for %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("ramune: package %q not found in npm registry", name)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ramune: registry returned %d for %s", resp.StatusCode, name)
	}

	var meta registryMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("ramune: failed to parse registry response for %s: %w", name, err)
	}
	return &meta, nil
}

// ResolvePackage resolves a package name and version range to a specific version.
func ResolvePackage(name, versionRange string) (*ResolvedPackage, error) {
	meta, err := fetchRegistryMetadata(name)
	if err != nil {
		return nil, err
	}

	if versionRange == "" || versionRange == "*" || versionRange == "latest" {
		if v, ok := meta.DistTags["latest"]; ok {
			versionRange = v
		}
	} else if v, ok := meta.DistTags[versionRange]; ok {
		versionRange = v
	}

	versions := make([]string, 0, len(meta.Versions))
	for v := range meta.Versions {
		versions = append(versions, v)
	}

	matched, err := bestMatch(versions, versionRange)
	if err != nil {
		return nil, fmt.Errorf("ramune: no version of %s matches %q", name, versionRange)
	}

	info := meta.Versions[matched]

	// Check platform compatibility (os/cpu fields).
	if !platformMatch(info.OS, info.CPU) {
		return nil, fmt.Errorf("ramune: package %s@%s is not compatible with %s/%s", name, info.Version, runtime.GOOS, runtime.GOARCH)
	}

	return &ResolvedPackage{
		Name:                 name,
		Version:              info.Version,
		Integrity:            info.Dist.Integrity,
		Tarball:              info.Dist.Tarball,
		Dependencies:         info.Dependencies,
		OptionalDependencies: info.OptionalDependencies,
	}, nil
}

// platformMatch checks if the package's os/cpu constraints match the current platform.
// Empty os/cpu means the package is compatible with all platforms.
func platformMatch(osField, cpuField []string) bool {
	if len(osField) > 0 {
		npmOS := runtime.GOOS
		if npmOS == "windows" {
			npmOS = "win32"
		}
		found := false
		for _, o := range osField {
			if o == npmOS {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(cpuField) > 0 {
		npmCPU := runtime.GOARCH
		switch npmCPU {
		case "amd64":
			npmCPU = "x64"
		case "386":
			npmCPU = "ia32"
		}
		found := false
		for _, c := range cpuField {
			if c == npmCPU {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type queueItem struct {
	name, rng string
	optional  bool
}

// resolveAll resolves all transitive dependencies via BFS.
func resolveAll(specs map[string]string) (map[string]*ResolvedPackage, error) {
	resolved := make(map[string]*ResolvedPackage)
	var queue []queueItem

	for name, rng := range specs {
		queue = append(queue, queueItem{name, rng, false})
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if _, exists := resolved[item.name]; exists {
			continue
		}

		pkg, err := ResolvePackage(item.name, item.rng)
		if err != nil {
			if item.optional {
				continue // skip optional deps that fail (wrong platform, not found, etc.)
			}
			return nil, err
		}
		pkg.Optional = item.optional
		resolved[item.name] = pkg

		for depName, depRange := range pkg.Dependencies {
			if _, exists := resolved[depName]; !exists {
				queue = append(queue, queueItem{depName, depRange, item.optional})
			}
		}
		for depName, depRange := range pkg.OptionalDependencies {
			if _, exists := resolved[depName]; !exists {
				queue = append(queue, queueItem{depName, depRange, true})
			}
		}
	}

	return resolved, nil
}

// ResolveAndInstall resolves all packages and their dependencies,
// then downloads them in parallel to nodeModulesDir.
func ResolveAndInstall(specs []string, nodeModulesDir string) (map[string]*ResolvedPackage, error) {
	specMap := make(map[string]string, len(specs))
	for _, spec := range specs {
		name, version := ParsePackageSpec(spec)
		specMap[name] = version
	}

	resolved, err := resolveAll(specMap)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(nodeModulesDir, 0o755); err != nil {
		return nil, err
	}

	pkgs := make([]*ResolvedPackage, 0, len(resolved))
	for _, p := range resolved {
		pkgs = append(pkgs, p)
	}
	return resolved, downloadAll(pkgs, nodeModulesDir)
}

// InstallFromLockfile downloads packages from a lockfile without re-resolving.
func InstallFromLockfile(lf *Lockfile, nodeModulesDir string) error {
	if err := os.MkdirAll(nodeModulesDir, 0o755); err != nil {
		return err
	}

	pkgs := make([]*ResolvedPackage, 0, len(lf.Packages))
	for _, e := range lf.Packages {
		pkgs = append(pkgs, &ResolvedPackage{
			Name: e.Name, Version: e.Version,
			Integrity: e.Integrity, Tarball: e.Tarball,
		})
	}
	return downloadAll(pkgs, nodeModulesDir)
}

// downloadAll downloads packages in parallel with bounded concurrency.
func downloadAll(pkgs []*ResolvedPackage, nodeModulesDir string) error {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, pkg := range pkgs {
		wg.Add(1)
		go func(p *ResolvedPackage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := downloadAndExtractPackage(p, nodeModulesDir); err != nil {
				if p.Optional {
					return // skip failed optional deps silently
				}
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(pkg)
	}
	wg.Wait()
	return firstErr
}

func downloadAndExtractPackage(pkg *ResolvedPackage, nodeModulesDir string) error {
	destDir := filepath.Join(nodeModulesDir, pkg.Name)

	// Skip if already installed.
	if _, err := os.Stat(filepath.Join(destDir, "package.json")); err == nil {
		return nil
	}

	resp, err := registryClient.Get(pkg.Tarball)
	if err != nil {
		return fmt.Errorf("ramune: download %s@%s: %w", pkg.Name, pkg.Version, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("ramune: download %s@%s: HTTP %d", pkg.Name, pkg.Version, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ramune: download %s@%s: %w", pkg.Name, pkg.Version, err)
	}

	if pkg.Integrity != "" {
		if err := verifyIntegrity(data, pkg.Integrity); err != nil {
			return fmt.Errorf("ramune: %s@%s integrity check failed: %w", pkg.Name, pkg.Version, err)
		}
	}

	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("ramune: %s@%s: invalid gzip: %w", pkg.Name, pkg.Version, err)
	}
	defer gr.Close()

	cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("ramune: %s@%s: tar read: %w", pkg.Name, pkg.Version, err)
		}

		// Strip the "package/" prefix from tarball paths.
		name := header.Name
		if idx := strings.Index(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if name == "" || name == "." {
			continue
		}

		target := filepath.Join(destDir, name)

		// Path traversal protection.
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanDest) &&
			filepath.Clean(target) != filepath.Clean(destDir) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("ramune: %s@%s: extract %s: %w", pkg.Name, pkg.Version, name, err)
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}

	return nil
}

func verifyIntegrity(data []byte, integrity string) error {
	if strings.HasPrefix(integrity, "sha512-") {
		expected, err := base64.StdEncoding.DecodeString(integrity[7:])
		if err != nil {
			return fmt.Errorf("ramune: malformed integrity hash: %w", err)
		}
		h := sha512.Sum512(data)
		if !bytes.Equal(h[:], expected) {
			return fmt.Errorf("ramune: sha512 mismatch")
		}
		return nil
	}
	return nil
}

// ParsePackageSpec splits "lodash@4" into ("lodash", "4").
// Scoped packages like "@scope/pkg@1" are handled correctly.
func ParsePackageSpec(spec string) (name, version string) {
	s := spec
	if strings.HasPrefix(s, "@") {
		slashIdx := strings.Index(s, "/")
		if slashIdx < 0 {
			return s, "*"
		}
		rest := s[slashIdx+1:]
		if atIdx := strings.Index(rest, "@"); atIdx >= 0 {
			return s[:slashIdx+1+atIdx], rest[atIdx+1:]
		}
		return s, "*"
	}
	if atIdx := strings.Index(s, "@"); atIdx > 0 {
		return s[:atIdx], s[atIdx+1:]
	}
	return s, "*"
}

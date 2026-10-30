// Package tools implementa as ferramentas que os agentes podem invocar
// (read_file, write_file, list_files, run_command, git_diff), todas confinadas
// ao diretório raiz do repositório alvo.
package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot é retornado quando um caminho tenta escapar da raiz do projeto.
var ErrOutsideRoot = errors.New("caminho fora do diretório raiz do projeto")

// Sandbox resolve caminhos fornecidos pelo LLM garantindo que fiquem dentro de Root.
type Sandbox struct {
	root string // absoluto, com symlinks resolvidos
}

// NewSandbox cria um Sandbox para o diretório informado (precisa existir).
func NewSandbox(root string) (*Sandbox, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("repo inválido: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repo %q não é um diretório", root)
	}
	return &Sandbox{root: real}, nil
}

// Root retorna o caminho absoluto da raiz.
func (s *Sandbox) Root() string { return s.root }

// Resolve converte um caminho relativo (à raiz) em absoluto, rejeitando
// caminhos absolutos, travessias com ".." e symlinks que apontem para fora.
// O alvo não precisa existir (útil para write_file).
func (s *Sandbox) Resolve(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: byte nulo no caminho", ErrOutsideRoot)
	}
	if p == "" {
		p = "."
	}
	if filepath.IsAbs(p) {
		// Aceita absolutos apenas se já estiverem dentro da raiz.
		rel, err := filepath.Rel(s.root, filepath.Clean(p))
		if err != nil || !isLocal(rel) {
			return "", fmt.Errorf("%w: %s", ErrOutsideRoot, p)
		}
		p = rel
	}
	clean := filepath.Clean(p)
	if !isLocal(clean) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, p)
	}
	full := filepath.Join(s.root, clean)

	// Resolve symlinks na parte existente do caminho para impedir escapes
	// do tipo repo/link -> /etc.
	real, err := evalExistingPrefix(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.root, real)
	if err != nil || !isLocal(rel) {
		return "", fmt.Errorf("%w: %s (symlink)", ErrOutsideRoot, p)
	}
	return real, nil
}

// Rel retorna o caminho relativo à raiz (para exibição).
func (s *Sandbox) Rel(abs string) string {
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

func isLocal(rel string) bool {
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// evalExistingPrefix resolve symlinks do maior prefixo existente de p e
// reanexa o restante (ainda inexistente).
func evalExistingPrefix(p string) (string, error) {
	var rest []string
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			parts := append([]string{real}, rest...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

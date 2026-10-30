package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------- read_file

// ReadFile lê um arquivo (opcionalmente um intervalo de linhas, 1-indexado e inclusivo).
type ReadFile struct{ env *Env }

func (*ReadFile) Name() string { return "read_file" }
func (*ReadFile) Description() string {
	return "Lê um arquivo de texto do repositório. Retorna as linhas prefixadas com o número da linha. " +
		"Use start_line/end_line (1-indexados, inclusivos) para arquivos grandes."
}
func (*ReadFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":       map[string]any{"type": "string", "description": "Caminho relativo à raiz do repositório"},
			"start_line": map[string]any{"type": "integer", "description": "Primeira linha (1-indexada). Opcional."},
			"end_line":   map[string]any{"type": "integer", "description": "Última linha (inclusiva). Opcional; 0 = até o fim."},
		},
		"required": []string{"path"},
	}
}

type readFileInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func (t *ReadFile) Run(_ context.Context, input json.RawMessage) (string, error) {
	var in readFileInput
	if err := decode(input, &in); err != nil {
		return "", err
	}
	if in.Path == "" {
		return "", errors.New("path é obrigatório")
	}
	abs, err := t.env.Sandbox.Resolve(in.Path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()

	start := max(in.StartLine, 1)
	end := in.EndLine
	if end != 0 && end < start {
		return "", fmt.Errorf("end_line (%d) menor que start_line (%d)", end, start)
	}

	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n, total := 0, 0
	for sc.Scan() {
		n++
		if n < start || (end != 0 && n > end) {
			continue
		}
		line := sc.Text()
		if strings.ContainsRune(line, 0) {
			return "", errors.New("arquivo parece ser binário")
		}
		total += len(line)
		if total > t.env.maxOut() {
			fmt.Fprintf(&b, "...[truncado na linha %d; use start_line/end_line]\n", n)
			break
		}
		fmt.Fprintf(&b, "%6d\t%s\n", n, line)
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if b.Len() == 0 {
		if n == 0 {
			return "(arquivo vazio)", nil
		}
		return "", fmt.Errorf("intervalo fora do arquivo (o arquivo tem %d linhas)", n)
	}
	return b.String(), nil
}

// --------------------------------------------------------------- write_file

// WriteFile cria ou sobrescreve um arquivo com o conteúdo informado.
type WriteFile struct{ env *Env }

func (*WriteFile) Name() string { return "write_file" }
func (*WriteFile) Description() string {
	return "Cria ou sobrescreve um arquivo com o conteúdo completo informado. Cria diretórios intermediários. " +
		"Sempre envie o arquivo inteiro, não apenas um trecho."
}
func (*WriteFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Caminho relativo à raiz do repositório"},
			"content": map[string]any{"type": "string", "description": "Conteúdo completo do arquivo"},
		},
		"required": []string{"path", "content"},
	}
}

type writeFileInput struct {
	Path    *string `json:"path"`
	Content *string `json:"content"`
}

func (t *WriteFile) Run(ctx context.Context, input json.RawMessage) (string, error) {
	var in writeFileInput
	if err := decode(input, &in); err != nil {
		return "", err
	}
	// Ponteiros distinguem "ausente" de "vazio": uma entrada truncada no
	// streaming não pode virar um arquivo zerado silenciosamente.
	if in.Path == nil || *in.Path == "" || in.Content == nil {
		return "", errors.New("path e content são obrigatórios")
	}
	abs, err := t.env.Sandbox.Resolve(*in.Path)
	if err != nil {
		return "", err
	}
	rel := t.env.Sandbox.Rel(abs)
	if rel == "." {
		return "", errors.New("path deve apontar para um arquivo")
	}

	info, statErr := os.Stat(abs)
	exists := statErr == nil
	if exists && info.IsDir() {
		return "", fmt.Errorf("%s é um diretório", rel)
	}
	if reason := t.env.Policy.WriteSensitivity(rel, exists); reason != "" {
		err := t.env.approve(ctx, ApprovalRequest{
			Tool:    t.Name(),
			Action:  fmt.Sprintf("write_file %s (%d bytes)", rel, len(*in.Content)),
			Reason:  reason,
			Details: truncate(*in.Content, 4000),
		})
		if err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	mode := os.FileMode(0o644)
	if exists {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(abs, []byte(*in.Content), mode); err != nil {
		return "", err
	}
	verb := "criado"
	if exists {
		verb = "sobrescrito"
	}
	return fmt.Sprintf("%s %s (%d bytes)", rel, verb, len(*in.Content)), nil
}

// --------------------------------------------------------------- list_files

// ListFiles lista arquivos recursivamente, com filtro glob opcional.
type ListFiles struct{ env *Env }

func (*ListFiles) Name() string { return "list_files" }
func (*ListFiles) Description() string {
	return "Lista arquivos recursivamente a partir de um diretório do repositório (ignora .git, node_modules, vendor). " +
		"pattern é um glob aplicado ao nome do arquivo (ex.: \"*.go\") ou ao caminho relativo se contiver '/' (ex.: \"internal/*/*.go\")."
}
func (*ListFiles) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Diretório relativo à raiz (padrão \".\")"},
			"pattern": map[string]any{"type": "string", "description": "Glob opcional"},
		},
	}
}

type listFilesInput struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".idea": true, "dist": true, "build": true, "target": true}

const maxListEntries = 1000

func (t *ListFiles) Run(ctx context.Context, input json.RawMessage) (string, error) {
	var in listFilesInput
	if err := decode(input, &in); err != nil {
		return "", err
	}
	if in.Pattern != "" {
		if _, err := path.Match(in.Pattern, ""); err != nil {
			return "", fmt.Errorf("pattern inválido: %w", err)
		}
	}
	abs, err := t.env.Sandbox.Resolve(in.Path)
	if err != nil {
		return "", err
	}
	var out []string
	truncated := false
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // ignora entradas ilegíveis
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != abs && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel := t.env.Sandbox.Rel(p)
		if in.Pattern != "" {
			target := d.Name()
			if strings.Contains(in.Pattern, "/") {
				target = rel
			}
			if ok, _ := path.Match(in.Pattern, target); !ok {
				return nil
			}
		}
		if len(out) >= maxListEntries {
			truncated = true
			return filepath.SkipAll
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "(nenhum arquivo encontrado)", nil
	}
	res := strings.Join(out, "\n")
	if truncated {
		res += fmt.Sprintf("\n...[lista truncada em %d entradas; refine path/pattern]", maxListEntries)
	}
	return res, nil
}

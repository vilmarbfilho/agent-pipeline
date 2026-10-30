package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newEnv(t *testing.T, approver Approver) (*Env, string) {
	t.Helper()
	dir := t.TempDir()
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &Env{Sandbox: sb, Approver: approver, Policy: DefaultPolicy(), CommandTimeout: 10 * time.Second}, sb.Root()
}

func run(t *testing.T, tool Tool, input any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Run(context.Background(), raw)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------------------------------ Sandbox

func TestSandboxResolve(t *testing.T) {
	env, root := newEnv(t, nil)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "inside/a.txt", "x")
	if err := os.Symlink(filepath.Join(root, "inside"), filepath.Join(root, "ok-link")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path    string
		wantErr bool
	}{
		{"a.txt", false},
		{"./dir/../a.txt", false},
		{"novo/dir/arquivo.go", false}, // inexistente, mas dentro
		{"", false},
		{"ok-link/a.txt", false}, // symlink que aponta para dentro
		{"../etc/passwd", true},
		{"dir/../../x", true},
		{"..", true},
		{"/etc/passwd", true},
		{filepath.Join(root, "a.txt"), false}, // absoluto dentro da raiz
		{"escape/segredo", true},              // symlink para fora
		{"escape/novo/arquivo", true},         // symlink para fora, alvo inexistente
		{"a\x00b", true},
	}
	for _, c := range cases {
		got, err := env.Sandbox.Resolve(c.path)
		if (err != nil) != c.wantErr {
			t.Errorf("Resolve(%q) = %q, err=%v; wantErr=%v", c.path, got, err, c.wantErr)
		}
		if c.wantErr && err != nil && !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Resolve(%q): esperado ErrOutsideRoot, veio %v", c.path, err)
		}
		if err == nil && !strings.HasPrefix(got, root) {
			t.Errorf("Resolve(%q) = %q fora de %q", c.path, got, root)
		}
	}
}

// ---------------------------------------------------------------- read_file

func TestReadFile(t *testing.T) {
	env, root := newEnv(t, nil)
	writeFile(t, root, "f.txt", "um\ndois\ntres\nquatro\n")
	rf := &ReadFile{env}

	out, err := run(t, rf, map[string]any{"path": "f.txt"})
	if err != nil || !strings.Contains(out, "1\tum") || !strings.Contains(out, "4\tquatro") {
		t.Fatalf("leitura completa: %q %v", out, err)
	}
	out, err = run(t, rf, map[string]any{"path": "f.txt", "start_line": 2, "end_line": 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "um") || strings.Contains(out, "quatro") || !strings.Contains(out, "2\tdois") || !strings.Contains(out, "3\ttres") {
		t.Fatalf("intervalo 2-3: %q", out)
	}
	if _, err := run(t, rf, map[string]any{"path": "f.txt", "start_line": 10}); err == nil {
		t.Fatal("esperava erro para intervalo fora do arquivo")
	}
	if _, err := run(t, rf, map[string]any{"path": "f.txt", "start_line": 3, "end_line": 2}); err == nil {
		t.Fatal("esperava erro para end < start")
	}
	if _, err := run(t, rf, map[string]any{"path": "../fora.txt"}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("path traversal deveria falhar: %v", err)
	}
	if _, err := run(t, rf, map[string]any{"path": "nao-existe"}); err == nil {
		t.Fatal("esperava erro para arquivo inexistente")
	}
}

// --------------------------------------------------------------- write_file

func TestWriteFile(t *testing.T) {
	env, root := newEnv(t, DenyAll)
	wf := &WriteFile{env}

	// Escrita comum: sem confirmação, cria diretórios.
	out, err := run(t, wf, map[string]any{"path": "pkg/x/x.go", "content": "package x\n"})
	if err != nil || !strings.Contains(out, "criado") {
		t.Fatalf("criação: %q %v", out, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "pkg/x/x.go"))
	if string(b) != "package x\n" {
		t.Fatalf("conteúdo: %q", b)
	}
	out, err = run(t, wf, map[string]any{"path": "pkg/x/x.go", "content": ""})
	if err != nil || !strings.Contains(out, "sobrescrito") {
		t.Fatalf("sobrescrita com conteúdo vazio deve ser permitida: %q %v", out, err)
	}

	// Escritas sensíveis são negadas pelo DenyAll.
	for _, p := range []string{".env", ".git/config", ".github/workflows/ci.yml", "certs/server.pem", "go.mod"} {
		if _, err := run(t, wf, map[string]any{"path": p, "content": "x"}); err == nil || !strings.Contains(err.Error(), "negada") {
			t.Errorf("escrita sensível em %s deveria pedir confirmação e ser negada: %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s não deveria ter sido criado", p)
		}
	}

	// Path traversal.
	if _, err := run(t, wf, map[string]any{"path": "../../evil", "content": "x"}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("traversal: %v", err)
	}
	// Entrada incompleta (ex.: streaming truncado) não pode zerar arquivo.
	if _, err := run(t, wf, map[string]any{"path": "pkg/x/x.go"}); err == nil {
		t.Fatal("content ausente deveria falhar")
	}
	if _, err := wf.Run(context.Background(), json.RawMessage(`{"path":"a","content":"tru`)); err == nil {
		t.Fatal("JSON truncado deveria falhar")
	}
}

func TestWriteFileApprovalFlow(t *testing.T) {
	var asked []ApprovalRequest
	approver := ApproverFunc(func(_ context.Context, r ApprovalRequest) (bool, error) {
		asked = append(asked, r)
		return true, nil
	})
	env, root := newEnv(t, approver)
	env.Policy.ConfirmAllWrites = true
	wf := &WriteFile{env}

	if _, err := run(t, wf, map[string]any{"path": "a.txt", "content": "oi"}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0].Tool != "write_file" || asked[0].Details != "oi" {
		t.Fatalf("aprovação esperada: %+v", asked)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal("arquivo aprovado deveria existir")
	}
}

// --------------------------------------------------------------- list_files

func TestListFiles(t *testing.T) {
	env, root := newEnv(t, nil)
	writeFile(t, root, "main.go", "")
	writeFile(t, root, "internal/a/a.go", "")
	writeFile(t, root, "internal/a/a_test.go", "")
	writeFile(t, root, "README.md", "")
	writeFile(t, root, "node_modules/lib/x.go", "")
	writeFile(t, root, ".git/HEAD", "")
	lf := &ListFiles{env}

	out, err := run(t, lf, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go", "internal/a/a.go", "README.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %s em %q", want, out)
		}
	}
	if strings.Contains(out, "node_modules") || strings.Contains(out, ".git/") {
		t.Errorf("diretórios ignorados apareceram: %q", out)
	}

	out, _ = run(t, lf, map[string]any{"pattern": "*_test.go"})
	if strings.TrimSpace(out) != "internal/a/a_test.go" {
		t.Errorf("pattern por nome: %q", out)
	}
	out, _ = run(t, lf, map[string]any{"path": "internal", "pattern": "internal/*/a.go"})
	if strings.TrimSpace(out) != "internal/a/a.go" {
		t.Errorf("pattern por caminho: %q", out)
	}
	if _, err := run(t, lf, map[string]any{"path": ".."}); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("traversal: %v", err)
	}
	if _, err := run(t, lf, map[string]any{"pattern": "["}); err == nil {
		t.Error("pattern inválido deveria falhar")
	}
}

// -------------------------------------------------------------- run_command

func TestRunCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("usa utilitários unix")
	}
	env, root := newEnv(t, DenyAll)
	writeFile(t, root, "hello.txt", "conteudo")
	rc := &RunCommand{env}

	out, err := run(t, rc, map[string]any{"command": "cat", "args": []string{"hello.txt"}})
	if err != nil || !strings.Contains(out, "exit_code: 0") || !strings.Contains(out, "conteudo") {
		t.Fatalf("cat: %q %v", out, err)
	}
	// Exit code != 0 não é erro de ferramenta; stderr é capturado.
	out, err = run(t, rc, map[string]any{"command": "ls", "args": []string{"nao-existe"}})
	if err != nil || strings.Contains(out, "exit_code: 0") || !strings.Contains(out, "--- stderr ---\nls") {
		t.Fatalf("ls inexistente: %q %v", out, err)
	}
	// Comandos destrutivos/fora da lista pedem confirmação (negada).
	for _, c := range []map[string]any{
		{"command": "rm", "args": []string{"hello.txt"}},
		{"command": "git", "args": []string{"push"}},
		{"command": "git", "args": []string{"-c", "alias.x=!rm", "x"}},
		{"command": "find", "args": []string{".", "-delete"}},
		{"command": "/bin/ls"},
		{"command": "sleep", "args": []string{"1"}},
	} {
		if _, err := run(t, rc, c); err == nil || !strings.Contains(err.Error(), "negada") {
			t.Errorf("%v deveria ser negado: %v", c, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "hello.txt")); err != nil {
		t.Fatal("hello.txt não deveria ter sido removido")
	}
	// Sem shell: metacaracteres no command são rejeitados.
	if _, err := run(t, rc, map[string]any{"command": "ls; rm -rf /"}); err == nil {
		t.Fatal("injeção de shell deveria falhar")
	}
}

func TestRunCommandTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("usa utilitários unix")
	}
	env, _ := newEnv(t, AutoApprove)
	env.CommandTimeout = 300 * time.Millisecond
	rc := &RunCommand{env}
	start := time.Now()
	out, err := run(t, rc, map[string]any{"command": "sleep", "args": []string{"5"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "TIMEOUT") || time.Since(start) > 4*time.Second {
		t.Fatalf("timeout não respeitado (%s): %q", time.Since(start), out)
	}
}

func TestRunCommandHidesSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("usa utilitários unix")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-secreta")
	env, _ := newEnv(t, AutoApprove)
	out, err := run(t, &RunCommand{env}, map[string]any{"command": "env"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sk-secreta") {
		t.Fatal("API key vazou para o subprocesso")
	}
}

// ----------------------------------------------------------------- git_diff

func TestGitDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	env, root := newEnv(t, nil)
	gd := &GitDiff{env}
	if _, err := run(t, gd, map[string]any{}); err == nil {
		t.Fatal("diretório sem git deveria falhar")
	}

	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitCmd("init", "-q")
	writeFile(t, root, "a.txt", "linha original\n")
	gitCmd("add", ".")
	gitCmd("commit", "-q", "-m", "init")

	out, err := run(t, gd, map[string]any{})
	if err != nil || !strings.Contains(out, "sem alterações") {
		t.Fatalf("repo limpo: %q %v", out, err)
	}

	writeFile(t, root, "a.txt", "linha alterada\n")
	writeFile(t, root, "novo.txt", "arquivo novo\n")
	out, err = run(t, gd, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-linha original", "+linha alterada", "novo.txt", "+arquivo novo"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff sem %q:\n%s", want, out)
		}
	}
}

// ------------------------------------------------------------------- Policy

func TestCommandRisk(t *testing.T) {
	p := DefaultPolicy()
	safe := [][]string{{"go", "test", "./..."}, {"npm", "test"}, {"git", "status"}, {"git", "diff"}, {"ls", "-la"}}
	for _, c := range safe {
		if r := p.CommandRisk(c[0], c[1:]); r != "" {
			t.Errorf("%v deveria ser seguro: %s", c, r)
		}
	}
	risky := [][]string{{"rm", "-rf", "x"}, {"git", "reset", "--hard"}, {"git", "commit"}, {"git"}, {"curl", "x"}, {"terraform", "apply"}, {"go", "clean", "-f"}, {"find", ".", "-exec", "rm", "{}", ";"}}
	for _, c := range risky {
		if r := p.CommandRisk(c[0], c[1:]); r == "" {
			t.Errorf("%v deveria exigir confirmação", c)
		}
	}
}

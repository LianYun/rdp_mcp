// Command rdp-mcp is the RDP MCP service and CLI. See docs/design.md §6.2.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"github.com/LianYun/rdp_mcp/internal/adminapi"
	"github.com/LianYun/rdp_mcp/internal/audit"
	"github.com/LianYun/rdp_mcp/internal/config"
	"github.com/LianYun/rdp_mcp/internal/events"
	"github.com/LianYun/rdp_mcp/internal/mcpserver"
	"github.com/LianYun/rdp_mcp/internal/runtime"
	"github.com/LianYun/rdp_mcp/internal/secrets"
	"github.com/LianYun/rdp_mcp/internal/session"
)

var version = "0.1.0-dev"

const usage = `rdp-mcp - control Windows machines over RDP via MCP

Usage:
  rdp-mcp serve [--config PATH] [--listen ADDR] [--worker PATH] [--log-level LEVEL]
  rdp-mcp status [--config PATH]
  rdp-mcp stop [--config PATH]
  rdp-mcp check-config [--config PATH]
  rdp-mcp secret set <machine|key>      store a password in the OS keychain
  rdp-mcp secret delete <machine|key>
  rdp-mcp trust <machine> [--config PATH]   forget the stored certificate (re-trust on next connect)
  rdp-mcp version

Config lookup: --config, ./.rdp_mcp/configs.yaml, ~/.rdp_mcp/configs.yaml
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(args)
	case "status":
		err = cmdStatus(args)
	case "stop":
		err = cmdStop(args)
	case "check-config":
		err = cmdCheckConfig(args)
	case "secret":
		err = cmdSecret(args)
	case "trust":
		err = cmdTrust(args)
	case "version", "--version", "-v":
		fmt.Println("rdp-mcp", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// dotenvWarnings collects .env messages so serve can log them once logging is set up.
var dotenvWarnings []string

func loadConfig(path string) (*config.Config, error) {
	p := config.Find(path)
	cfg, err := config.Load(p)
	if err != nil {
		return nil, err
	}
	// Load .env (next to the config and in data_dir), then re-parse so ${VAR} in server fields resolve.
	loaded, warnings := config.LoadDotEnv(p, cfg.Server.EffectiveDataDir())
	dotenvWarnings = warnings
	for _, f := range loaded {
		dotenvWarnings = append(dotenvWarnings, "loaded environment from "+f)
	}
	if len(loaded) > 0 {
		if cfg, err = config.Load(p); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config file")
	listen := fs.String("listen", "", "override server.listen")
	workerPath := fs.String("worker", "", "override server.worker_path")
	logLevel := fs.String("log-level", "", "override server.log_level")
	managedBy := fs.String("managed-by", envOr("RDP_MCP_MANAGED_BY", "cli"), "cli | ui-sidecar | launchd | systemd")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Server.Listen = *listen
	}
	if *workerPath != "" {
		cfg.Server.WorkerPath = *workerPath
	}
	if *logLevel != "" {
		cfg.Server.LogLevel = *logLevel
	}
	warnings, err := cfg.Validate()
	if err != nil {
		return fmt.Errorf("invalid config %s:\n%w", cfg.Path, err)
	}

	dataDir := cfg.Server.EffectiveDataDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dataDir, "logs", "rdp-mcp.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	level := new(slog.LevelVar)
	level.Set(parseLevel(cfg.Server.LogLevel))
	log := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logFile), &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)
	for _, w := range warnings {
		log.Warn("config: " + w)
	}
	for _, w := range dotenvWarnings {
		log.Info(".env: " + w)
	}

	rt, err := runtime.Acquire(dataDir)
	if err != nil {
		if errors.Is(err, runtime.ErrAlreadyRunning) {
			if info, ok, _ := runtime.ReadInfo(dataDir); ok {
				return fmt.Errorf("%w (pid %d, listening on %s)", err, info.PID, info.Listen)
			}
		}
		return err
	}
	defer rt.Release()

	wp, err := session.FindWorker(cfg.Server.WorkerPath)
	if err != nil {
		return err
	}

	adminToken := cfg.Server.AdminToken
	if adminToken == "" {
		adminToken = runtime.NewToken()
	}
	addr := cfg.Server.EffectiveListen()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	bus := events.NewBus()
	al := audit.New(filepath.Join(dataDir, "audit"), cfg.Server.AuditEnabled(), cfg.Server.Audit.RedactTypedText,
		cfg.Server.AuditRetentionDays(), bus)
	mgr := session.NewManager(cfg, session.Options{
		WorkerPath:  wp,
		DataDir:     dataDir,
		Logger:      log,
		Bus:         bus,
		KeychainGet: secrets.Get,
		WorkerDebug: strings.EqualFold(cfg.Server.LogLevel, "debug"),
	})

	var mcpTokenMu sync.RWMutex
	mcpToken := cfg.Server.MCPToken
	getMCPToken := func() string { mcpTokenMu.RLock(); defer mcpTokenMu.RUnlock(); return mcpToken }

	mcpSrv := mcpserver.New(mgr, al, version).MCP()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)

	started := time.Now()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	al.SetThumbnails(cfg.Server.AuditScreenshots())
	// applyCfg activates a new config, whether written by the admin API or edited on disk.
	applyCfg := func(c *config.Config) {
		if *listen != "" {
			c.Server.Listen = *listen
		}
		if *workerPath != "" {
			c.Server.WorkerPath = *workerPath
		}
		mgr.UpdateConfig(c)
		mcpTokenMu.Lock()
		mcpToken = c.Server.MCPToken
		mcpTokenMu.Unlock()
		level.Set(parseLevel(c.Server.LogLevel))
		al.SetThumbnails(c.Server.AuditScreenshots())
	}

	api := &adminapi.API{
		Mgr: mgr, Bus: bus, Audit: al, AdminToken: adminToken, MCPToken: getMCPToken,
		Origins: cfg.Server.CORSOrigins, Shutdown: stop,
		Keychain: secrets.Keychain{}, Apply: applyCfg,
		LogFile: filepath.Join(dataDir, "logs", "rdp-mcp.log"),
		Info: adminapi.ServiceInfo{
			Version: version, PID: os.Getpid(), Listen: ln.Addr().String(),
			MCPURL:     "http://" + ln.Addr().String() + "/mcp",
			ConfigPath: cfg.Path, DataDir: dataDir, ManagedBy: *managedBy, StartedAt: started, WorkerPath: wp,
		},
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpAuth(getMCPToken, mcpHandler))
	mux.Handle("/mcp/", mcpAuth(getMCPToken, mcpHandler))
	mux.Handle("/api/", api.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "rdp-mcp %s\nMCP endpoint: /mcp\nAdmin API: /api/v1\n", version)
	})
	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	if err := rt.WriteInfo(runtime.Info{
		PID: os.Getpid(), Listen: ln.Addr().String(), AdminToken: adminToken, Version: version,
		StartedAt: started, ConfigPath: cfg.Path, ManagedBy: *managedBy,
	}); err != nil {
		return err
	}

	mgr.Start()
	go watchConfig(ctx, cfg.Path, *listen, *workerPath, mgr, bus, log, applyCfg)

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()
	log.Info("rdp-mcp started", "version", version, "mcp", "http://"+ln.Addr().String()+"/mcp",
		"config", cfg.Path, "machines", len(cfg.Machines), "worker", wp, "data_dir", dataDir)
	bus.Publish("service", map[string]any{"status": "started", "version": version})

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
		}
	}
	log.Info("shutting down")
	bus.Publish("service", map[string]any{"status": "stopping"})
	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shCtx)
	mgr.Shutdown()
	log.Info("stopped")
	return nil
}

func mcpAuth(token func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := token(); t != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(t)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="rdp-mcp"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// watchConfig polls the config file and applies valid changes.
func watchConfig(ctx context.Context, path, listenOverride, workerOverride string, mgr *session.Manager,
	bus *events.Bus, log *slog.Logger, apply func(*config.Config)) {
	var lastMod time.Time
	var lastSize int64 = -1
	if st, err := os.Stat(path); err == nil {
		lastMod, lastSize = st.ModTime(), st.Size()
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if st.ModTime().Equal(lastMod) && st.Size() == lastSize {
			continue
		}
		lastMod, lastSize = st.ModTime(), st.Size()
		cfg, err := config.Load(path)
		if err == nil {
			if listenOverride != "" {
				cfg.Server.Listen = listenOverride
			}
			if workerOverride != "" {
				cfg.Server.WorkerPath = workerOverride
			}
			var warnings []string
			warnings, err = cfg.Validate()
			for _, w := range warnings {
				log.Warn("config: " + w)
			}
		}
		if err != nil {
			log.Error("config reload failed; keeping previous config", "err", err)
			bus.Publish("config_error", map[string]string{"error": err.Error()})
			continue
		}
		old := mgr.Config()
		if old.Server.EffectiveListen() != cfg.Server.EffectiveListen() || old.Server.DataDir != cfg.Server.DataDir {
			log.Warn("server.listen/data_dir changed; restart rdp-mcp to apply")
		}
		apply(cfg)
		log.Info("config reloaded", "machines", len(cfg.Machines))
		bus.Publish("config_changed", map[string]any{"machines": len(cfg.Machines)})
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func dataDirFor(cfgPath string) (string, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return "", err
	}
	return cfg.Server.EffectiveDataDir(), nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config file")
	_ = fs.Parse(args)
	dd, err := dataDirFor(*cfgPath)
	if err != nil {
		return err
	}
	info, ok, err := runtime.ReadInfo(dd)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("rdp-mcp is not running")
		os.Exit(3)
	}
	fmt.Printf("rdp-mcp %s running (pid %d, managed by %s, since %s)\nMCP endpoint: http://%s/mcp\nconfig: %s\n",
		info.Version, info.PID, info.ManagedBy, info.StartedAt.Format(time.RFC3339), info.Listen, info.ConfigPath)
	req, _ := http.NewRequest("GET", "http://"+info.Listen+"/api/v1/machines", nil)
	req.Header.Set("Authorization", "Bearer "+info.AdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Printf("machines: %s\n", b)
	return nil
}

func cmdStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config file")
	_ = fs.Parse(args)
	dd, err := dataDirFor(*cfgPath)
	if err != nil {
		return err
	}
	info, ok, err := runtime.ReadInfo(dd)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("rdp-mcp is not running")
		return nil
	}
	req, _ := http.NewRequest("POST", "http://"+info.Listen+"/api/v1/service/shutdown", nil)
	req.Header.Set("Authorization", "Bearer "+info.AdminToken)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	} else {
		_ = syscall.Kill(info.PID, syscall.SIGTERM)
	}
	for i := 0; i < 50; i++ {
		if syscall.Kill(info.PID, 0) != nil {
			fmt.Println("stopped")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("pid %d did not exit", info.PID)
}

func cmdCheckConfig(args []string) error {
	fs := flag.NewFlagSet("check-config", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config file")
	_ = fs.Parse(args)
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.Path); err != nil {
		return fmt.Errorf("config file %s not found", cfg.Path)
	}
	fmt.Println("config:", cfg.Path)
	for _, w := range dotenvWarnings {
		fmt.Println(".env:", w)
	}
	warnings, err := cfg.Validate()
	for _, w := range warnings {
		fmt.Println("warning:", w)
	}
	if err != nil {
		return err
	}
	for _, m := range cfg.Machines {
		r := cfg.Resolve(m)
		pw := "ok"
		if _, err := config.ResolvePassword(context.Background(), m, secrets.Get); err != nil {
			pw = "ERROR: " + err.Error()
		}
		fmt.Printf("  %-16s %s@%s:%d  %dx%d  security=%s cert=%s  password: %s\n",
			m.Name, m.Username, m.Host, r.Port, r.Width, r.Height, r.Security, r.Cert, pw)
	}
	if wp, err := session.FindWorker(cfg.Server.WorkerPath); err != nil {
		fmt.Println("worker: ERROR:", err)
	} else {
		fmt.Println("worker:", wp)
	}
	fmt.Println("OK")
	return nil
}

func cmdSecret(args []string) error {
	if len(args) != 2 || (args[0] != "set" && args[0] != "delete") {
		return errors.New("usage: rdp-mcp secret set|delete <key>")
	}
	key := args[1]
	if args[0] == "delete" {
		return secrets.Delete(key)
	}
	fmt.Fprintf(os.Stderr, "Password for %q: ", key)
	var pw []byte
	var err error
	if term.IsTerminal(int(os.Stdin.Fd())) {
		pw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
	} else {
		pw, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		return err
	}
	if err := secrets.Set(key, strings.TrimRight(string(pw), "\r\n")); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "stored; reference it in configs.yaml as: password: keychain:%s\n", key)
	return nil
}

func cmdTrust(args []string) error {
	fs := flag.NewFlagSet("trust", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config file")
	if len(args) < 1 {
		return errors.New("usage: rdp-mcp trust <machine>")
	}
	name := args[0]
	_ = fs.Parse(args[1:])
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	m, ok := cfg.Machine(name)
	if !ok {
		return fmt.Errorf("unknown machine %q", name)
	}
	r := cfg.Resolve(m)
	kh := session.NewKnownHosts(filepath.Join(cfg.Server.EffectiveDataDir(), "known_hosts"))
	if err := kh.Delete(r.Host + ":" + strconv.Itoa(r.Port)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Printf("forgot certificate for %s:%d; the next connection will trust the presented certificate\n", r.Host, r.Port)
	return nil
}

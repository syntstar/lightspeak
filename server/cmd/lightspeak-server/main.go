// Command lightspeak-server runs the Lightspeak API/gateway and offers a small
// admin CLI that works directly on the SQLite database.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/syntstar/lightspeak/server/internal/auth"
	"github.com/syntstar/lightspeak/server/internal/config"
	"github.com/syntstar/lightspeak/server/internal/server"
	"github.com/syntstar/lightspeak/server/internal/store"
)

var version = "dev"

const usage = `Usage:
  lightspeak-server serve [--dev]
  lightspeak-server migrate [--dev]
  lightspeak-server version
  lightspeak-server check-config [--dev]   validate the settings and print them (no secrets)
  lightspeak-server admin [--dev] <command>

Admin commands:
  bootstrap [--force]                 one-time owner invite (24 h, grants server admin)
  create-invite [--hours N] [--uses N] [--space ID]
  reset-password <username>           prints a temporary password, revokes sessions
  disable-user <username> | enable-user <username>
  list-users
  storage-report
  purge-attachments --days N
  vacuum
  backup [--keep 3]
`

func main() {
	server.Version = version
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "migrate":
		err = cmdMigrate(os.Args[2:])
	case "admin":
		err = cmdAdmin(os.Args[2:])
	case "check-config":
		err = cmdCheckConfig(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// cmdCheckConfig validates the environment without touching the database, so
// install.sh can test a new configuration before restarting the service.
func cmdCheckConfig(args []string) error {
	fs := flag.NewFlagSet("check-config", flag.ContinueOnError)
	dev := fs.Bool("dev", false, "development mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*dev)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, s := range cfg.Settings() {
		fmt.Fprintf(tw, "%s\t%s\n", s[0], s[1])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, k := range config.UnknownEnv() {
		fmt.Fprintf(os.Stderr, "warning: unknown setting %s is ignored\n", k)
	}
	return nil
}

func open(ctx context.Context, dev bool) (*config.Config, *store.DB, error) {
	cfg, err := config.Load(dev)
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, err
	}
	db, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	return cfg, db, nil
}

// applyMemoryLimit sets the Go soft memory limit to 85 % of the systemd
// MemoryMax (cgroup v2), so the GC works harder before the kernel kills the
// service. GOMEMLIMIT in the environment wins. Returns the limit or 0.
func applyMemoryLimit() int64 {
	if os.Getenv("GOMEMLIMIT") != "" {
		return 0
	}
	// cgroup v2: "0::/system.slice/lightspeak-server.service"
	self, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0
	}
	_, path, ok := strings.Cut(strings.TrimSpace(string(self)), "::")
	if !ok || strings.Contains(path, "..") {
		return 0
	}
	b, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", path, "memory.max"))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n <= 0 {
		return 0 // "max" = unlimited
	}
	limit := n / 100 * 85
	debug.SetMemoryLimit(limit)
	return limit
}

func newLogger(dev bool) *slog.Logger {
	if dev {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dev := fs.Bool("dev", false, "development mode")
	_ = fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, db, err := open(ctx, *dev)
	if err != nil {
		return err
	}
	defer db.Close()
	log := newLogger(*dev)
	if limit := applyMemoryLimit(); limit > 0 {
		log.Info("memory limit from cgroup", "goMemLimitMiB", limit>>20)
	}
	if host, _, _ := net.SplitHostPort(cfg.Addr); !*dev && host != "127.0.0.1" && host != "::1" && host != "localhost" {
		log.Warn("API listens on a non-loopback address; it is meant to sit behind Caddy", "addr", cfg.Addr)
	}
	srv := server.New(cfg, db, log)
	defer srv.Close()
	srv.Start(ctx)

	api := &http.Server{Addr: cfg.Addr, Handler: srv.Router(), ReadTimeout: 10 * time.Minute, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10}
	hook := &http.Server{Addr: cfg.WebhookAddr, Handler: srv.WebhookRouter(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second}
	errc := make(chan error, 2)
	go func() { errc <- api.ListenAndServe() }()
	go func() { errc <- hook.ListenAndServe() }()
	log.Info("lightspeak-server started", "version", version, "addr", cfg.Addr, "webhook", cfg.WebhookAddr, "dev", *dev)
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hook.Shutdown(sctx)
	return api.Shutdown(sctx)
}

func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dev := fs.Bool("dev", false, "development mode")
	_ = fs.Parse(args)
	_, db, err := open(context.Background(), *dev)
	if err != nil {
		return err
	}
	fmt.Println("migrations applied")
	return db.Close()
}

func cmdAdmin(args []string) error {
	dev := false
	if len(args) > 0 && args[0] == "--dev" {
		dev, args = true, args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx := context.Background()
	cfg, db, err := open(ctx, dev)
	if err != nil {
		return err
	}
	defer db.Close()
	srv := server.New(cfg, db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer srv.Close()
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "bootstrap":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		force := fs.Bool("force", false, "create even if an admin exists")
		_ = fs.Parse(rest)
		var n int
		if err := db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_server_admin=1`).Scan(&n); err != nil {
			return err
		}
		if n > 0 && !*force {
			return errors.New("a server admin already exists (use --force to create another owner invite)")
		}
		code, url, err := srv.CreateOwnerInvite(ctx, 24*3600)
		if err != nil {
			return err
		}
		fmt.Printf("Owner invite (valid 24 h, single use; earlier unused owner invites are now invalid):\n  %s\n  code: %s\n", url, code)
	case "create-invite":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		hours := fs.Int64("hours", 168, "validity in hours (0 = never expires)")
		uses := fs.Int64("uses", 1, "max uses (0 = unlimited)")
		space := fs.Int64("space", 0, "space ID to join (0 = registration only)")
		_ = fs.Parse(rest)
		code, url, err := srv.CreateInvite(ctx, *space, *hours*3600, *uses, false)
		if err != nil {
			return err
		}
		fmt.Printf("%s\n  code: %s\n", url, code)
	case "reset-password":
		if len(rest) != 1 {
			return errors.New("usage: reset-password <username>")
		}
		pw := auth.RandomCode(16)
		hash, err := auth.Hash(pw, auth.Params{MemoryKiB: cfg.ArgonMemoryKiB, Time: cfg.ArgonTime, Threads: 1})
		if err != nil {
			return err
		}
		res, err := db.W.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE username=?`, hash, strings.ToLower(rest[0]))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.New("unknown user")
		}
		if _, err := db.W.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE user_id=(SELECT id FROM users WHERE username=?) AND revoked_at IS NULL`,
			time.Now().UnixMilli(), strings.ToLower(rest[0])); err != nil {
			return err
		}
		fmt.Printf("Temporary password for %s: %s\nAsk the user to change it under Settings → My Account.\n", rest[0], pw)
	case "disable-user", "enable-user":
		if len(rest) != 1 {
			return fmt.Errorf("usage: %s <username>", cmd)
		}
		now := time.Now().UnixMilli()
		var q string
		var args []any
		if cmd == "disable-user" {
			q, args = `UPDATE users SET disabled_at=? WHERE username=?`, []any{now, strings.ToLower(rest[0])}
		} else {
			q, args = `UPDATE users SET disabled_at=NULL WHERE username=?`, []any{strings.ToLower(rest[0])}
		}
		res, err := db.W.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.New("unknown user")
		}
		if cmd == "disable-user" {
			_, _ = db.W.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE user_id=(SELECT id FROM users WHERE username=?) AND revoked_at IS NULL`, now, strings.ToLower(rest[0]))
		}
		fmt.Println("ok")
	case "list-users":
		rows, err := db.R.QueryContext(ctx, `SELECT u.username, u.display_name, u.is_server_admin, u.disabled_at IS NOT NULL, u.created_at,
			(SELECT MAX(last_used_at) FROM sessions WHERE user_id=u.id) FROM users u ORDER BY u.created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "USERNAME\tDISPLAY NAME\tADMIN\tDISABLED\tCREATED\tLAST SEEN")
		for rows.Next() {
			var u, d string
			var admin, disabled bool
			var created int64
			var seen *int64
			if err := rows.Scan(&u, &d, &admin, &disabled, &created, &seen); err != nil {
				return err
			}
			ls := "-"
			if seen != nil {
				ls = time.UnixMilli(*seen).Format("2006-01-02 15:04")
			}
			fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%s\t%s\n", u, d, admin, disabled, time.UnixMilli(created).Format("2006-01-02"), ls)
		}
		return tw.Flush()
	case "storage-report":
		rep, err := srv.Storage(ctx, 20)
		if err != nil {
			return err
		}
		fmt.Printf("Attachments: %d files, %s of %s quota\n", rep.FileCount, human(rep.UsedBytes), human(rep.QuotaBytes))
		for _, it := range rep.Largest {
			fmt.Printf("  %10s  %s  (id %s)\n", human(it.Size), it.Filename, it.ID)
		}
	case "purge-attachments":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		days := fs.Int("days", 0, "delete attachments older than N days")
		_ = fs.Parse(rest)
		if *days <= 0 {
			return errors.New("--days N required")
		}
		total := 0
		for {
			n, err := srv.PurgeAttachmentsOlderThan(ctx, *days)
			if err != nil {
				return err
			}
			total += n
			if n == 0 {
				break
			}
		}
		fmt.Printf("deleted %d attachments\n", total)
	case "vacuum":
		if _, err := db.W.ExecContext(ctx, `VACUUM`); err != nil {
			return err
		}
		fmt.Println("ok")
	case "backup":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		keep := fs.Int("keep", 3, "generations to keep")
		_ = fs.Parse(rest)
		dst := filepath.Join(cfg.BackupDir(), "lightspeak-"+time.Now().UTC().Format("20060102-150405")+".db")
		if err := db.Backup(ctx, dst); err != nil {
			return err
		}
		if err := os.Chmod(dst, 0o640); err != nil {
			return err
		}
		matches, _ := filepath.Glob(filepath.Join(cfg.BackupDir(), "lightspeak-*.db"))
		sort.Strings(matches)
		for len(matches) > *keep {
			_ = os.Remove(matches[0])
			matches = matches[1:]
		}
		fmt.Println("backup written:", dst)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	return nil
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

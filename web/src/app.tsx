import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import {
  Activity,
  ArrowDownUp,
  ChevronRight,
  Database,
  FileText,
  Globe2,
  LayoutDashboard,
  ListFilter,
  LogOut,
  Moon,
  Network,
  RefreshCw,
  Settings2,
  ShieldCheck,
  Sun,
  Users,
} from "lucide-react";
import { api, historyWindow, type Row, type Settings } from "./lib/api";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./components/ui/dialog";
import { Details, ErrorNotice } from "./components/data";
import Overview from "./features/overview";
import Queries from "./features/queries";
import Configuration from "./features/configuration";
import SettingsView from "./features/settings";
const Diagnostics = lazy(() => import("./features/diagnostics"));
const Jobs = lazy(() => import("./features/settings/jobs"));
const navigation = [
  ["overview", "Overview", LayoutDashboard],
  ["queries", "Query log", ListFilter],
  ["clients", "Clients", Users],
  ["lists", "Filter lists", ShieldCheck],
  ["rules", "Custom rules", FileText],
  ["records", "DNS records", Globe2],
  ["upstreams", "Upstreams", ArrowDownUp],
  ["settings", "Settings", Settings2],
  ["jobs", "Backup & jobs", Database],
  ["diagnostics", "Diagnostics", Activity],
] as const;
function initialPage() {
  const p = location.pathname.split("/")[1];
  return navigation.some((n) => n[0] === p) ? p : "overview";
}
export default function App() {
  const [page, setPage] = useState(initialPage);
  const [range, setRange] = useState("24h");
  const [anchor, setAnchor] = useState(() => Date.now());
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [custom, setCustom] = useState<{ from: string; to: string }>();
  const [rangeError, setRangeError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const liveTick = useCallback(() => setAnchor(Date.now()), []);
  const [filter, setFilter] = useState<Record<string, string>>({});
  const [auth, setAuth] = useState(false);
  const [dark, setDark] = useState(
    () => localStorage.getItem("theme") === "dark",
  );
  const [error, setError] = useState<Error>();
  const [blocking, setBlocking] = useState(false);
  useEffect(() => {
    const expire = () => setAuth(true);
    const pop = () => setPage(initialPage());
    window.addEventListener("session-expired", expire);
    window.addEventListener("popstate", pop);
    return () => {
      window.removeEventListener("session-expired", expire);
      window.removeEventListener("popstate", pop);
    };
  }, []);
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("theme", dark ? "dark" : "light");
  }, [dark]);
  function navigate(p: string) {
    setPage(p);
    history.pushState({}, "", p === "overview" ? "/" : "/" + p);
  }
  const { params: rangeParams, resolution } = historyWindow(
    range,
    anchor,
    custom,
  );
  const title = navigation.find((n) => n[0] === page)?.[1] ?? "Overview";
  return (
    <div className="app-shell">
      <a className="skip" href="#main">
        Skip to content
      </a>
      <aside className="sidebar">
        <a
          className="brand"
          href="/"
          onClick={(e) => {
            e.preventDefault();
            navigate("overview");
          }}
        >
          <span className="brand-mark">
            <Network size={21} />
          </span>
          <span>
            dimsum<small>DNS administration</small>
          </span>
        </a>
        <nav aria-label="Main navigation">
          {navigation.map(([id, label, Icon], i) => (
            <Button
              key={id}
              variant="ghost"
              className={
                "nav-item " +
                (page === id ? "selected " : "") +
                (i === 3 || i === 7 ? "group-start" : "")
              }
              onClick={() => navigate(id)}
              aria-current={page === id ? "page" : undefined}
            >
              <Icon size={17} />
              <span>{label}</span>
              {page === id && <ChevronRight size={14} />}
            </Button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <p>
            One network.
            <br />
            One filtering policy.
          </p>
          <Button variant="ghost" onClick={() => setDark(!dark)}>
            {dark ? <Sun size={16} /> : <Moon size={16} />}{" "}
            {dark ? "Light" : "Dark"} appearance
          </Button>
          <Button
            variant="ghost"
            onClick={async () => {
              try {
                await api.logout();
                setAuth(true);
              } catch (e) {
                setError(e as Error);
              }
            }}
          >
            <LogOut size={16} /> Sign out
          </Button>
        </div>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <div>
            <span className="muted">Network</span>
            <ChevronRight size={14} />
            <span>{title}</span>
          </div>
          <Button variant="outline" onClick={() => setBlocking(true)}>
            <ShieldCheck size={15} /> Blocking controls
          </Button>
        </header>
        <main id="main">
          <div className="page-heading">
            <div>
              <h1>{title}</h1>
              <p>
                {page === "overview"
                  ? "A clear view of your network’s DNS traffic."
                  : page === "queries"
                    ? "Inspect requests and understand each decision."
                    : "Network-wide administration"}
              </p>
            </div>
            <div className="actions">
              <label className="sr-only" htmlFor="range">
                Time range
              </label>
              <select
                id="range"
                value={range}
                onChange={(e) => {
                  setRange(e.target.value);
                  setAnchor(Date.now());
                }}
              >
                <option value="1h">Last hour</option>
                <option value="24h">Last 24 hours</option>
                <option value="7d">Last 7 days</option>
                <option value="custom">Custom range</option>
              </select>
              <Button
                variant="outline"
                aria-label="Refresh all data"
                onClick={() => {
                  setRefresh((v) => v + 1);
                  setAnchor(Date.now());
                }}
              >
                <RefreshCw size={15} />
              </Button>
            </div>
          </div>
          {range === "custom" && (
            <form
              className="inline-form custom-range"
              onSubmit={(e) => {
                e.preventDefault();
                if (
                  !from ||
                  !to ||
                  new Date(from) >= new Date(to) ||
                  Date.parse(to) - Date.parse(from) > 366 * 86400000
                ) {
                  setRangeError(
                    "Choose a positive range no longer than 366 days.",
                  );
                  return;
                }
                setCustom({
                  from: new Date(from).toISOString(),
                  to: new Date(to).toISOString(),
                });
                setRangeError("");
              }}
            >
              <label>
                From
                <Input
                  type="datetime-local"
                  required
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                />
              </label>
              <label>
                To
                <Input
                  type="datetime-local"
                  required
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
                />
              </label>
              <Button>Use range</Button>
              {rangeError && <span role="alert">{rangeError}</span>}
            </form>
          )}
          <div className="range-caption">
            {new Date(
              new URLSearchParams(rangeParams).get("from")!,
            ).toLocaleString()}{" "}
            –{" "}
            {new Date(
              new URLSearchParams(rangeParams).get("to")!,
            ).toLocaleString()}
            {" · Missing and partial intervals are marked"}
          </div>
          {error && <ErrorNotice error={error} />}
          <Suspense fallback={<p role="status">Loading view…</p>}>
            {page === "overview" ? (
              <Overview
                resolution={resolution}
                range={rangeParams}
                refresh={refresh}
                drill={(key, value) => {
                  setFilter({ [key]: value });
                  navigate("queries");
                }}
              />
            ) : page === "queries" ? (
              <Queries
                onLiveTick={liveTick}
                key={JSON.stringify(filter)}
                range={rangeParams}
                refresh={refresh}
                initialFilter={filter}
              />
            ) : page === "settings" ? (
              <SettingsView key={refresh} />
            ) : page === "diagnostics" ? (
              <Diagnostics key={refresh} />
            ) : page === "jobs" ? (
              <Jobs key={refresh} />
            ) : (
              <Configuration
                key={page + refresh}
                kind={page}
                range={rangeParams}
              />
            )}
          </Suspense>
          <footer>
            dimsum{" "}
            <span>Configuration is text. Changes are revision checked.</span>
          </footer>
        </main>
      </div>
      <Dialog open={auth}>
        <DialogContent
          showCloseButton={false}
          onEscapeKeyDown={(e) => e.preventDefault()}
          onPointerDownOutside={(e) => e.preventDefault()}
        >
          <DialogTitle>Sign in to dimsum</DialogTitle>
          <DialogDescription>
            Your session is required to inspect or change DNS administration.
          </DialogDescription>
          <Login
            onSuccess={() => {
              setAuth(false);
              setRefresh((v) => v + 1);
            }}
          />
        </DialogContent>
      </Dialog>
      <Dialog open={blocking} onOpenChange={setBlocking}>
        <DialogContent>
          <DialogTitle>Network-wide blocking</DialogTitle>
          <DialogDescription>
            A pause affects every client. Choose an explicit expiry.
          </DialogDescription>
          <Blocking />
        </DialogContent>
      </Dialog>
    </div>
  );
}
function Login({ onSuccess }: { onSuccess: () => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(undefined);
        try {
          await api.login(password);
          setPassword("");
          onSuccess();
        } catch (err) {
          setError(err as Error);
        } finally {
          setBusy(false);
        }
      }}
    >
      <label>
        Admin password
        <Input
          autoFocus
          autoComplete="current-password"
          type="password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      {error && <ErrorNotice error={error} />}
      <Button disabled={busy}>{busy ? "Signing in…" : "Sign in"}</Button>
    </form>
  );
}
function Blocking() {
  const [minutes, setMinutes] = useState("5");
  const [result, setResult] = useState<Row>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  async function update(enabled: boolean) {
    setBusy(true);
    setError(undefined);
    try {
      const settings = await api.get<Settings>("settings");
      setResult(
        await api.send<Row>("blocking", "PUT", {
          revision: settings.revision,
          enabled,
          ...(!enabled
            ? {
                pause_until: new Date(
                  Date.now() + Number(minutes) * 60000,
                ).toISOString(),
              }
            : {}),
        }),
      );
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <label>
        Pause duration
        <select value={minutes} onChange={(e) => setMinutes(e.target.value)}>
          <option value="5">5 minutes</option>
          <option value="30">30 minutes</option>
          <option value="60">1 hour</option>
        </select>
      </label>
      <div className="actions">
        <Button disabled={busy} variant="outline" onClick={() => update(false)}>
          Pause blocking
        </Button>
        <Button disabled={busy} onClick={() => update(true)}>
          Resume blocking
        </Button>
      </div>
      {error && <ErrorNotice error={error} />}{" "}
      {result && <Details value={result} />}
    </>
  );
}

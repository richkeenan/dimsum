import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import {
  Link,
  useNavigate,
  useRouterState,
  useSearch,
} from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ArrowDownUp,
  Database,
  FileText,
  Globe2,
  Gauge,
  LayoutDashboard,
  ListFilter,
  LogOut,
  Menu,
  Moon,
  RefreshCw,
  Settings2,
  ShieldCheck,
  Sun,
  Users,
  X,
} from "lucide-react";
import { api, historyWindow, type Row, type Settings } from "./lib/api";
import { filterKeys, type ViewSearch } from "./lib/navigation";
import { useLive } from "./lib/hooks";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./components/ui/dialog";
import { ErrorNotice } from "./components/data";
import { DNSAddresses, ServiceNotices } from "./components/network-status";
import Overview from "./features/overview";
import Performance from "./features/performance";
import logo from "./assets/dimsum.svg";
import Queries from "./features/queries";
import Configuration from "./features/configuration";
import SettingsView from "./features/settings";
const Diagnostics = lazy(() => import("./features/diagnostics"));
const Jobs = lazy(() => import("./features/settings/jobs"));
const navigation = [
  ["overview", "Overview", LayoutDashboard],
  ["performance", "Performance", Gauge],
  ["queries", "Query log", ListFilter],
  ["clients", "Devices", Users],
  ["lists", "Filter lists", ShieldCheck],
  ["rules", "Custom rules", FileText],
  ["records", "Local DNS", Globe2],
  ["upstreams", "Upstreams", ArrowDownUp],
  ["settings", "Settings", Settings2],
  ["jobs", "Backups", Database],
  ["diagnostics", "Diagnostics", Activity],
] as const;

export default function App() {
  const path = useRouterState({ select: (s) => s.location.pathname });
  const page = path.split("/")[1] || "overview";
  const search = useSearch({ strict: false }) as ViewSearch;
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const range = search.range ?? "24h";
  const custom =
    range === "custom" && search.from && search.to
      ? { from: search.from, to: search.to }
      : undefined;
  const filter = useMemo(
    () =>
      Object.fromEntries(
        filterKeys.filter((k) => search[k]).map((k) => [k, search[k]!]),
      ),
    [search],
  );
  const [anchor, setAnchor] = useState(() => Date.now());
  const [refresh, setRefresh] = useState(0);
  const [auth, setAuth] = useState(
    () =>
      typeof sessionStorage === "undefined" ||
      !sessionStorage.getItem("dimsum-csrf"),
  );
  const [dark, setDark] = useState(
    () =>
      typeof localStorage !== "undefined" &&
      localStorage.getItem("theme") === "dark",
  );
  const [menu, setMenu] = useState(false);
  const [blocking, setBlocking] = useState(false);
  const [error, setError] = useState<Error>();
  const [customOpen, setCustomOpen] = useState(range === "custom");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [rangeError, setRangeError] = useState("");
  useEffect(() => {
    const localInput = (value?: string) => {
      if (!value) return "";
      const date = new Date(value);
      if (!Number.isFinite(date.getTime())) return "";
      return new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
        .toISOString()
        .slice(0, 16);
    };
    setCustomOpen(range === "custom");
    setFrom(localInput(search.from));
    setTo(localInput(search.to));
    setRangeError("");
  }, [range, search.from, search.to]);
  const liveTick = useCallback(() => setAnchor(Date.now()), []);
  useLive(
    !auth &&
      ["overview", "performance", "clients"].includes(page) &&
      range !== "custom",
    liveTick,
    5000,
  );
  const historical = ["overview", "performance", "queries", "clients"].includes(
    page,
  );
  const title = navigation.find((n) => n[0] === page)?.[1] ?? "Overview";
  const { params: rangeParams, resolution } = historyWindow(
    range,
    anchor,
    custom,
  );
  const rangeSearch: ViewSearch = { range, ...(custom ?? {}) };
  function go(p: string, next: ViewSearch = rangeSearch) {
    setMenu(false);
    void navigate({ to: "/$page", params: { page: p }, search: next });
  }

  useEffect(() => {
    const expire = () => {
      setAuth(true);
      void queryClient.cancelQueries();
      queryClient.clear();
    };
    const changed = () => {
      void queryClient.invalidateQueries({ queryKey: ["api"] });
    };
    window.addEventListener("session-expired", expire);
    window.addEventListener("configuration-changed", changed);
    return () => {
      window.removeEventListener("session-expired", expire);
      window.removeEventListener("configuration-changed", changed);
    };
  }, [queryClient]);
  useEffect(() => {
    const style = document.createElement("style");
    style.textContent = "*,*::before,*::after{transition:none!important}";
    document.head.append(style);
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("theme", dark ? "dark" : "light");
    void document.documentElement.offsetHeight;
    const frame = requestAnimationFrame(() => style.remove());
    return () => {
      cancelAnimationFrame(frame);
      style.remove();
    };
  }, [dark]);
  useEffect(() => {
    document.title = `${title} · dimsum`;
    setMenu(false);
  }, [title]);

  if (auth)
    return (
      <div className="flex min-h-dvh flex-col items-center justify-center bg-[#172e50] p-6">
        <div className="mb-7 flex items-center gap-3 text-[28px] font-semibold text-white">
          <img src={logo} alt="" width={74} height={60} />
          <span>dimsum</span>
        </div>
        <section className="w-full max-w-100 rounded-[14px] bg-background p-6 sm:p-8 [&>h1]:text-2xl [&>h1]:font-semibold [&>p]:mt-2 [&>p]:text-muted-foreground [&>form]:my-6 [&>form>button]:w-full">
          <h1>Welcome to dimsum</h1>
          <p>Sign in to manage your network.</p>
          <Login
            onSuccess={() => {
              queryClient.clear();
              setAuth(false);
              setAnchor(Date.now());
            }}
          />
        </section>
      </div>
    );

  return (
    <div className="flex min-h-dvh">
      <a
        className="fixed -top-16 z-50 bg-background p-3 focus:top-0"
        href="#main"
      >
        Skip to content
      </a>
      {menu && (
        <button
          className="fixed inset-0 z-20 bg-[#071326]/55 md:hidden"
          aria-label="Close navigation"
          onClick={() => setMenu(false)}
        />
      )}
      <aside
        className={`fixed inset-y-0 left-0 z-30 w-65 flex-col overflow-y-auto bg-[#172e50] px-4 py-6 text-[#edf3ff] md:flex md:w-60 ${menu ? "flex" : "hidden"}`}
        aria-label="Application navigation"
      >
        <Link
          to="/"
          search={rangeSearch}
          className="flex items-center gap-2.5 px-2.5 pb-7 text-[25px] font-medium tracking-tight [&_small]:block [&_small]:whitespace-nowrap [&_small]:text-[12px] [&_small]:font-normal [&_small]:tracking-normal [&_small]:text-[#aebfda]"
        >
          <img src={logo} alt="" width={54} height={44} className="shrink-0" />
          <span>
            dimsum<small>DNS administration</small>
          </span>
        </Link>
        <button
          className="absolute right-3 top-4 p-2 md:hidden"
          aria-label="Close navigation"
          onClick={() => setMenu(false)}
        >
          <X size={20} />
        </button>
        <nav aria-label="Main navigation" className="flex flex-col gap-1">
          {navigation.map(([id, label, Icon]) => (
            <Link
              key={id}
              to="/$page"
              params={{ page: id }}
              search={rangeSearch}
              className={`flex min-h-11 items-center gap-2.5 rounded-md px-3 text-sm md:min-h-10 ${page === id ? "bg-[#2a4871] font-normal text-white" : "font-light text-[#c2d0e5] hover:bg-[#233e63] hover:text-white"} ${id === "lists" || id === "settings" ? "mt-5" : ""}`}
              aria-current={page === id ? "page" : undefined}
            >
              <Icon size={18} strokeWidth={1.5} />
              <span>{label}</span>
            </Link>
          ))}
        </nav>
        <div className="mt-5 border-t border-[#395778] pt-4 [&>button]:w-full [&>button]:justify-between [&>button]:font-normal [&>button]:text-[#c2d0e5] [&>button:hover]:bg-[#233e63] [&>button:hover]:text-white">
          <DNSAddresses />
        </div>
        <div className="mt-auto pt-8 [&>button]:w-full [&>button]:justify-start [&>button]:font-normal [&>button]:text-[#c2d0e5] [&>button_svg]:stroke-[1.5] [&>button:hover]:bg-[#233e63] [&>button:hover]:text-white">
          <Button variant="ghost" onClick={() => setDark(!dark)}>
            {dark ? <Sun size={16} /> : <Moon size={16} />}{" "}
            {dark ? "Light" : "Dark"} appearance
          </Button>
          <Button
            variant="ghost"
            onClick={async () => {
              try {
                await api.logout();
                queryClient.clear();
                setAuth(true);
              } catch (e) {
                setError(e as Error);
              }
            }}
          >
            <LogOut size={16} />
            Sign out
          </Button>
        </div>
      </aside>
      <div className="min-w-0 flex-1 md:ml-60">
        <header className="flex min-h-16 items-center border-b border-border bg-background px-4 py-2 text-xs md:hidden">
          <Button
            variant="ghost"
            aria-label="Open navigation"
            onClick={() => setMenu(true)}
          >
            <Menu size={20} />
          </Button>
        </header>
        <main id="main" className="mx-auto max-w-425 px-4 py-6 md:px-8 md:py-8">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-4">
            <div className="[&>h1]:text-[26px] [&>h1]:font-semibold [&>h1]:tracking-tight [&>p]:mt-1 [&>p]:text-xs [&>p]:text-muted-foreground">
              <h1>{title}</h1>
              {page === "overview" && <p>DNS activity across your network.</p>}
              {page === "performance" && (
                <p>How quickly DNS queries resolve, and where the time goes.</p>
              )}
            </div>
            <div className="flex items-center gap-2">
              {page === "lists" && (
                <Button variant="outline" onClick={() => setBlocking(true)}>
                  Pause filtering…
                </Button>
              )}
              {historical && (
                <>
                  <label className="sr-only" htmlFor="range">
                    Time range
                  </label>
                  <select
                    className="min-h-9 rounded-md border border-input bg-background px-3 py-2 text-sm"
                    id="range"
                    value={customOpen ? "custom" : range}
                    onChange={(e) => {
                      setCustomOpen(e.target.value === "custom");
                      if (e.target.value !== "custom") {
                        setAnchor(Date.now());
                        go(page, {
                          ...search,
                          range: e.target.value,
                          from: undefined,
                          to: undefined,
                        });
                      }
                    }}
                  >
                    <option value="1h">Last hour</option>
                    <option value="24h">Last 24 hours</option>
                    <option value="7d">Last 7 days</option>
                    <option value="custom">Custom range</option>
                  </select>
                </>
              )}
              <Button
                variant="outline"
                aria-label="Reload displayed data"
                title="Reload displayed data"
                onClick={() => {
                  setRefresh((v) => v + 1);
                  setAnchor(Date.now());
                  void queryClient.invalidateQueries({ queryKey: ["api"] });
                }}
              >
                <RefreshCw size={16} />
              </Button>
            </div>
          </div>
          {historical && customOpen && (
            <form
              className="mb-5 flex flex-wrap items-end gap-3 [&>label]:min-w-0 [&>label]:flex-1"
              onSubmit={(e) => {
                e.preventDefault();
                const start = Date.parse(from),
                  end = Date.parse(to);
                if (
                  !Number.isFinite(start) ||
                  !Number.isFinite(end) ||
                  start >= end ||
                  end - start > 366 * 86400000
                ) {
                  setRangeError("Choose a range of up to one year.");
                  return;
                }
                setRangeError("");
                go(page, {
                  ...search,
                  range: "custom",
                  from: new Date(start).toISOString(),
                  to: new Date(end).toISOString(),
                });
              }}
            >
              <label className="flex flex-col gap-1.5 text-xs font-normal">
                From
                <Input
                  type="datetime-local"
                  required
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                />
              </label>
              <label className="flex flex-col gap-1.5 text-xs font-normal">
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
          {historical && page !== "queries" && range === "custom" && (
            <div className="mb-5 text-xs text-muted-foreground">
              <span>
                {new Date(search.from!).toLocaleString()} –{" "}
                {new Date(search.to!).toLocaleString()}
              </span>
            </div>
          )}
          <ServiceNotices />
          {error && <ErrorNotice error={error} />}
          <Suspense
            fallback={
              <p
                className="my-4 rounded-lg bg-muted p-8 text-center text-muted-foreground"
                role="status"
              >
                Loading…
              </p>
            }
          >
            {page === "overview" ? (
              <Overview
                resolution={resolution}
                range={rangeParams}
                refresh={refresh}
                onPerformance={() => go("performance")}
                drill={(key, value) =>
                  go("queries", { ...rangeSearch, [key]: value })
                }
              />
            ) : page === "performance" ? (
              <Performance
                range={rangeParams}
                resolution={resolution}
                refresh={refresh}
              />
            ) : page === "queries" ? (
              <Queries
                key={`${range}:${search.from ?? ""}:${search.to ?? ""}`}
                onLiveTick={liveTick}
                range={rangeParams}
                refresh={refresh}
                initialFilter={filter}
                onFilterChange={(next) =>
                  go("queries", { ...rangeSearch, ...next })
                }
                liveAllowed={range !== "custom"}
              />
            ) : page === "settings" ? (
              <SettingsView />
            ) : page === "diagnostics" ? (
              <Diagnostics />
            ) : page === "jobs" ? (
              <Jobs />
            ) : (
              <Configuration
                key={page}
                kind={page}
                range={rangeParams}
                onClientQueries={(address) =>
                  go("queries", { ...rangeSearch, client: address })
                }
              />
            )}
          </Suspense>
        </main>
      </div>
      <Dialog open={blocking} onOpenChange={setBlocking}>
        <DialogContent>
          <DialogTitle>Pause filtering</DialogTitle>
          <DialogDescription>
            Temporarily stop blocking domains for all devices, for example to
            troubleshoot a website. DNS keeps working, and filtering resumes
            automatically after the selected duration.
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
      className="space-y-4"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(undefined);
        try {
          await api.login(password);
          setPassword("");
          onSuccess();
        } catch (e) {
          setError(e as Error);
        } finally {
          setBusy(false);
        }
      }}
    >
      <label className="flex flex-col gap-1.5 text-xs font-normal">
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
      <label className="flex flex-col gap-1.5 text-xs font-normal">
        Pause duration
        <select
          className="min-h-9 rounded-md border border-input bg-background px-3 py-2 text-sm"
          value={minutes}
          onChange={(e) => setMinutes(e.target.value)}
        >
          <option value="5">5 minutes</option>
          <option value="30">30 minutes</option>
          <option value="60">1 hour</option>
        </select>
      </label>
      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={busy} variant="outline" onClick={() => update(false)}>
          Pause filtering
        </Button>
      </div>
      {error && <ErrorNotice error={error} />}{" "}
      {result && (
        <p role="status">Pause saved. Filtering will resume automatically.</p>
      )}
    </>
  );
}

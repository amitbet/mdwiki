import { useCallback, useEffect, useRef, useState } from "react";
import WikiEditor, { type SpaceInfo } from "./WikiEditor";

const LS_SPACE = "mdwiki.space";
const LS_PAGE = "mdwiki.page";

type DeviceFlowState = {
  flow_id: string;
  user_code: string;
  verification_uri: string;
  open_uri: string;
  interval: number;
};

type SetupStatus = {
  configured: boolean;
  settings?: {
    root_repo_url?: string;
    root_repo_local_dir?: string;
    storage_dir?: string;
  };
};

type ThemeMode = "light" | "dark";

function readLS(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

function writeLS(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // ignore
  }
}

function LoginScreen({
  onLogin,
  onStartDevice,
  deviceBusy,
  deviceFlow,
  deviceError,
  onCancelDevice,
}: {
  onLogin: () => void;
  onStartDevice: () => void;
  deviceBusy: boolean;
  deviceFlow: DeviceFlowState | null;
  deviceError: string | null;
  onCancelDevice: () => void;
}) {
  return (
    <div className="setup-shell">
      <div className="setup-card">
        <h1>mdwiki</h1>
        <p>Sign in with GitHub to enable realtime collaboration and saved author identity.</p>

        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <button type="button" onClick={onLogin}>
            Login with GitHub
          </button>
          <button type="button" onClick={onStartDevice} disabled={deviceBusy || !!deviceFlow}>
            Sign in with device code
          </button>
        </div>

        {deviceError ? <p className="error">{deviceError}</p> : null}

        {deviceFlow ? (
          <div>
            <p style={{ margin: "8px 0" }}>
              Open{" "}
              <a href={deviceFlow.open_uri} target="_blank" rel="noreferrer">
                {deviceFlow.verification_uri}
              </a>{" "}
              and confirm this code:
            </p>
            <p style={{ fontFamily: "ui-monospace, monospace", fontSize: "1.4rem", margin: "8px 0" }}>
              {deviceFlow.user_code}
            </p>
            <button type="button" onClick={onCancelDevice}>
              Cancel
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function SetupScreen({
  initialSettings,
  onConfigured,
}: {
  initialSettings?: SetupStatus["settings"];
  onConfigured: () => void;
}) {
  const [rootRepoLocalDir, setRootRepoLocalDir] = useState(initialSettings?.root_repo_local_dir ?? "./data/root-git-repo");
  const [storageDir, setStorageDir] = useState(initialSettings?.storage_dir ?? "./data/storage");
  const [spaceKey, setSpaceKey] = useState("main");
  const [spaceName, setSpaceName] = useState("Main Space");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await fetch("/api/setup/init", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          root_repo_local_dir: rootRepoLocalDir,
          storage_dir: storageDir,
          first_space_key: spaceKey,
          first_space_name: spaceName,
        }),
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      onConfigured();
    } catch (e) {
      setError(e instanceof Error ? e.message : "setup failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="setup-shell">
      <div className="setup-card">
        <h1>Initial setup</h1>
        <p>Configure local paths and your first space. Root repo URL comes from server env.</p>

        <label>
          Root repo local directory
          <input
            value={rootRepoLocalDir}
            onChange={(e) => setRootRepoLocalDir(e.target.value)}
            placeholder="./data/root-git-repo"
          />
        </label>

        <label>
          Storage directory
          <input
            value={storageDir}
            onChange={(e) => setStorageDir(e.target.value)}
            placeholder="./data/storage"
          />
        </label>

        <label>
          First space key
          <input value={spaceKey} onChange={(e) => setSpaceKey(e.target.value.toLowerCase())} />
        </label>

        <label>
          First space name
          <input value={spaceName} onChange={(e) => setSpaceName(e.target.value)} />
        </label>

        <button type="button" onClick={() => void submit()} disabled={busy}>
          {busy ? "Creating…" : "Create wiki"}
        </button>
        {error ? <p className="error">{error}</p> : null}
      </div>
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState<{ login: string; name: string } | null>(null);
  const [setup, setSetup] = useState<SetupStatus | null>(null);
  const [spaces, setSpaces] = useState<SpaceInfo[] | null>(null);
  const [space, setSpace] = useState(() => readLS(LS_SPACE, "main"));
  const [path, setPath] = useState(() => readLS(LS_PAGE, "README.md"));
  const [deviceFlow, setDeviceFlow] = useState<DeviceFlowState | null>(null);
  const [deviceError, setDeviceError] = useState<string | null>(null);
  const [deviceBusy, setDeviceBusy] = useState(false);
  const [theme, setTheme] = useState<ThemeMode>(() => {
    try {
      const saved = localStorage.getItem("mdwiki.theme");
      return saved === "dark" ? "dark" : "light";
    } catch {
      return "light";
    }
  });
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem("mdwiki.theme", theme);
    } catch {
      // ignore
    }
  }, [theme]);

  const loadSession = useCallback(async () => {
    const r = await fetch("/api/session", { credentials: "include" });
    const j = (await r.json()) as { login?: string; name?: string } | null;
    if (j?.login) {
      setSession({ login: j.login, name: j.name ?? j.login });
    } else {
      setSession(null);
    }
  }, []);

  const loadSetup = useCallback(async () => {
    const r = await fetch("/api/setup/status", { credentials: "include" });
    const j = (await r.json()) as SetupStatus;
    setSetup(j);
  }, []);

  const loadSpaces = useCallback(async () => {
    const r = await fetch("/api/spaces", { credentials: "include" });
    const list = (await r.json()) as SpaceInfo[];
    setSpaces(Array.isArray(list) ? list : []);
  }, []);

  useEffect(() => {
    void loadSession();
  }, [loadSession]);

  useEffect(() => {
    if (!session) {
      setSetup(null);
      setSpaces(null);
      return;
    }
    void loadSetup();
  }, [loadSetup, session]);

  useEffect(() => {
    if (!session || !setup?.configured) {
      setSpaces(null);
      return;
    }
    void loadSpaces();
  }, [loadSpaces, session, setup]);

  useEffect(() => {
    if (!session || !setup?.configured) {
      return;
    }
    let stopped = false;
    const tick = async () => {
      if (stopped || document.hidden) {
        return;
      }
      await loadSpaces();
    };
    const id = window.setInterval(() => {
      void tick();
    }, 5000);
    const onFocus = () => {
      void loadSpaces();
    };
    const onVisibility = () => {
      if (!document.hidden) {
        void loadSpaces();
      }
    };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      stopped = true;
      window.clearInterval(id);
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [loadSpaces, session, setup]);

  useEffect(() => {
    if (!spaces || spaces.length === 0) return;
    if (!spaces.some((s) => s.key === space)) {
      setSpace(spaces[0].key);
      setPath("README.md");
    }
  }, [spaces, space]);

  useEffect(() => {
    writeLS(LS_SPACE, space);
  }, [space]);

  useEffect(() => {
    writeLS(LS_PAGE, path);
  }, [path]);

  const login = useCallback(() => {
    window.location.href = "/auth/github";
  }, []);

  const startDeviceLogin = useCallback(async () => {
    setDeviceError(null);
    setDeviceBusy(true);
    try {
      const r = await fetch("/auth/github/device/start", {
        method: "POST",
        credentials: "include",
      });
      if (!r.ok) {
        throw new Error(await r.text());
      }
      const j = (await r.json()) as Record<string, unknown>;
      if (typeof j.flow_id !== "string" || typeof j.user_code !== "string" || typeof j.verification_uri !== "string") {
        throw new Error("unexpected response from device start");
      }
      const complete =
        typeof j.verification_uri_complete === "string" && j.verification_uri_complete.length > 0
          ? j.verification_uri_complete
          : j.verification_uri;
      setDeviceFlow({
        flow_id: j.flow_id,
        user_code: j.user_code,
        verification_uri: j.verification_uri,
        open_uri: complete,
        interval: typeof j.interval === "number" ? Math.max(3, j.interval) : 5,
      });
    } catch (e) {
      setDeviceError(e instanceof Error ? e.message : "device start failed");
      setDeviceFlow(null);
    } finally {
      setDeviceBusy(false);
    }
  }, []);

  useEffect(() => {
    if (!deviceFlow) {
      return;
    }
    let cancelled = false;

    const schedule = (delayMs: number) => {
      if (pollTimerRef.current) {
        clearTimeout(pollTimerRef.current);
      }
      pollTimerRef.current = setTimeout(runPoll, delayMs);
    };

    const runPoll = async () => {
      if (cancelled) return;
      try {
        const r = await fetch(`/auth/github/device/poll?flow_id=${encodeURIComponent(deviceFlow.flow_id)}`, {
          credentials: "include",
        });
        if (r.status === 404 || r.status === 410) {
          setDeviceError("This sign-in request expired. Start again.");
          setDeviceFlow(null);
          return;
        }
        if (!r.ok) {
          setDeviceError(await r.text());
          setDeviceFlow(null);
          return;
        }
        const j = (await r.json()) as { status?: string; retry_after?: number; login?: string; name?: string };
        if (j.status === "complete" && j.login) {
          setSession({ login: j.login, name: j.name ?? j.login });
          setDeviceFlow(null);
          return;
        }
        let next = deviceFlow.interval * 1000;
        if (j.retry_after && j.retry_after > 0) {
          next = Math.max(next, j.retry_after * 1000);
        }
        schedule(next);
      } catch {
        setDeviceError("Polling failed; try again.");
        setDeviceFlow(null);
      }
    };

    schedule(deviceFlow.interval * 1000);

    return () => {
      cancelled = true;
      if (pollTimerRef.current) {
        clearTimeout(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    };
  }, [deviceFlow]);

  if (session === null) {
    return (
      <LoginScreen
        onLogin={login}
        onStartDevice={() => void startDeviceLogin()}
        deviceBusy={deviceBusy}
        deviceFlow={deviceFlow}
        deviceError={deviceError}
        onCancelDevice={() => {
          setDeviceFlow(null);
          setDeviceError(null);
        }}
      />
    );
  }

  if (setup === null) {
    return <div className="loading-screen">Loading…</div>;
  }

  if (!setup.configured) {
    return <SetupScreen initialSettings={setup.settings} onConfigured={() => void loadSetup()} />;
  }

  if (spaces === null) {
    return <div className="loading-screen">Loading spaces…</div>;
  }

  return (
    <WikiEditor
      spaces={spaces}
      space={space}
      onSpaceChange={(k) => {
        setSpace(k);
        setPath("README.md");
      }}
      onSpacesChanged={loadSpaces}
      currentUserLogin={session.login}
      path={path}
      onPathChange={setPath}
      theme={theme}
      onToggleTheme={() => setTheme((prev) => (prev === "light" ? "dark" : "light"))}
    />
  );
}

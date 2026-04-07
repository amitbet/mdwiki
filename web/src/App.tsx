import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
    /* ignore */
  }
}

export default function App() {
  const [session, setSession] = useState<{ login: string; name: string } | null>(null);
  const [spaces, setSpaces] = useState<SpaceInfo[] | null>(null);
  const [space, setSpace] = useState(() =>
    readLS(LS_SPACE, import.meta.env.VITE_SPACE_KEY ?? "demo"),
  );
  const [path, setPath] = useState(() => readLS(LS_PAGE, "README.md"));
  const [deviceFlow, setDeviceFlow] = useState<DeviceFlowState | null>(null);
  const [deviceError, setDeviceError] = useState<string | null>(null);
  const [deviceBusy, setDeviceBusy] = useState(false);
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    fetch("/api/session", { credentials: "include" })
      .then((r) => (r.ok ? r.json() : null))
      .then(setSession)
      .catch(() => setSession(null));
  }, []);

  useEffect(() => {
    if (!session) {
      setSpaces(null);
      return;
    }
    let cancelled = false;
    fetch("/api/spaces", { credentials: "include" })
      .then((r) => (r.ok ? r.json() : []))
      .then((list: SpaceInfo[]) => {
        if (!cancelled) setSpaces(Array.isArray(list) ? list : []);
      })
      .catch(() => {
        if (!cancelled) setSpaces([]);
      });
    return () => {
      cancelled = true;
    };
  }, [session]);

  useEffect(() => {
    if (!spaces || spaces.length === 0) return;
    if (!spaces.some((s) => s.key === space)) {
      setSpace(spaces[0].key);
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

  const onSpaceChange = useCallback((key: string) => {
    setSpace(key);
    setPath("README.md");
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
        const t = await r.text();
        throw new Error(t || r.statusText);
      }
      const j = (await r.json()) as Record<string, unknown>;
      const flowId = j.flow_id;
      const userCode = j.user_code;
      const verificationUri = j.verification_uri;
      const complete =
        typeof j.verification_uri_complete === "string" && j.verification_uri_complete.length > 0
          ? j.verification_uri_complete
          : null;
      if (
        typeof flowId !== "string" ||
        typeof userCode !== "string" ||
        typeof verificationUri !== "string"
      ) {
        throw new Error("unexpected response from device start");
      }
      const intervalSec = typeof j.interval === "number" ? j.interval : 5;
      setDeviceFlow({
        flow_id: flowId,
        user_code: userCode,
        verification_uri: verificationUri,
        open_uri: complete ?? verificationUri,
        interval: Math.max(3, intervalSec),
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
        const r = await fetch(
          `/auth/github/device/poll?flow_id=${encodeURIComponent(deviceFlow.flow_id)}`,
          { credentials: "include" },
        );
        if (r.status === 404 || r.status === 410) {
          setDeviceError("This sign-in request expired. Start again.");
          setDeviceFlow(null);
          return;
        }
        if (!r.ok) {
          const t = await r.text();
          setDeviceError(t || `HTTP ${r.status}`);
          setDeviceFlow(null);
          return;
        }
        const j = (await r.json()) as {
          status?: string;
          retry_after?: number;
          login?: string;
          name?: string;
        };
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

  const cancelDevice = useCallback(() => {
    setDeviceFlow(null);
    setDeviceError(null);
  }, []);

  const logout = useCallback(() => {
    document.cookie = "mdwiki_session=; Max-Age=0; path=/";
    setSession(null);
  }, []);

  const main = useMemo(() => {
    if (!session) {
      return (
        <div style={{ padding: 24 }}>
          <h1>mdwiki</h1>
          <p>Git-backed wiki (GFM + Yjs). Sign in with GitHub to edit.</p>
          <p style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
            <button type="button" onClick={login}>
              Login with GitHub
            </button>
            <button type="button" onClick={startDeviceLogin} disabled={deviceBusy || !!deviceFlow}>
              Sign in with device code
            </button>
          </p>
          {deviceError ? (
            <p role="alert" style={{ color: "#c33" }}>
              {deviceError}
            </p>
          ) : null}
          {deviceFlow ? (
            <div
              style={{
                marginTop: 16,
                padding: 16,
                maxWidth: 420,
                border: "1px solid #ccc",
                borderRadius: 8,
              }}
            >
              <p style={{ marginTop: 0 }}>
                Open{" "}
                <a href={deviceFlow.open_uri} target="_blank" rel="noreferrer">
                  {deviceFlow.verification_uri}
                </a>{" "}
                {deviceFlow.open_uri === deviceFlow.verification_uri ? "and enter:" : "(confirm the code if prompted)"}
              </p>
              <p
                style={{
                  fontSize: "1.5rem",
                  fontFamily: "ui-monospace, monospace",
                  letterSpacing: "0.05em",
                }}
              >
                {deviceFlow.user_code}
              </p>
              <button type="button" onClick={cancelDevice}>
                Cancel
              </button>
            </div>
          ) : null}
        </div>
      );
    }
    if (spaces === null) {
      return (
        <div style={{ padding: 24, color: "#8b949e" }}>
          Loading spaces…
        </div>
      );
    }
    return (
      <WikiEditor
        spaces={spaces}
        space={space}
        onSpaceChange={onSpaceChange}
        path={path}
        onPathChange={setPath}
        userName={session.name || session.login}
        onLogout={logout}
      />
    );
  }, [
    session,
    spaces,
    space,
    path,
    login,
    logout,
    onSpaceChange,
    startDeviceLogin,
    deviceBusy,
    deviceFlow,
    deviceError,
    cancelDevice,
  ]);

  return <div className="app">{main}</div>;
}

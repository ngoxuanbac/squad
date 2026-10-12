import { useEffect, useRef, useState } from "react";

/** How often ps --view is asked for while something is in a turn ... */
export const BUSY_MS = 3000;
/** ... and otherwise. */
export const QUIET_MS = 15000;

/** The web app's page; a native app has none. Typed here: the plugin typechecks without the DOM library. */
type Page = { visibilityState: string; addEventListener(type: string, fn: () => void): void; removeEventListener(type: string, fn: () => void): void };
const page = (globalThis as { document?: Page }).document;
const hidden = () => page?.visibilityState === "hidden";

export type Failed = { code: string; message: string };
export type Loaded<T> = {
  value: T | null;
  error: Failed | null;
  /** When the value was read (ms); null before the first good read. */
  at: number | null;
};
export type LoadResult<T> = { ok: true; value: T } | { ok: false; code: string; error: string };

/**
 * Calls `load` now and again after `every(value)` ms while mounted and the page is shown (a hidden
 * page waits, and asks at once when shown again). Keeps the last good value across a failure. A new
 * `key` starts over; `load` and `every` may be fresh closures each render.
 */
export function usePoll<T>(load: () => Promise<LoadResult<T>>, key: string, every: (value: T | null) => number): Loaded<T> {
  const [state, setState] = useState<Loaded<T>>({ value: null, error: null, at: null });
  const latest = useRef({ load, every });
  latest.current = { load, every };
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let last: T | null = null;
    const again = () => {
      if (live) timer = setTimeout(tick, hidden() ? QUIET_MS : latest.current.every(last));
    };
    const tick = async () => {
      if (hidden()) return again();
      try {
        const r = await latest.current.load();
        if (r.ok) last = r.value;
        if (live) setState((s) => (r.ok ? { value: r.value, error: null, at: Date.now() } : { ...s, error: { code: r.code, message: r.error } }));
      } catch (error) {
        if (live) setState((s) => ({ ...s, error: { code: "failed", message: error instanceof Error ? error.message : String(error) } }));
      }
      again();
    };
    const shown = () => {
      if (hidden() || !live) return;
      clearTimeout(timer);
      void tick();
    };
    setState({ value: null, error: null, at: null });
    void tick();
    page?.addEventListener("visibilitychange", shown);
    return () => {
      live = false;
      clearTimeout(timer);
      page?.removeEventListener("visibilitychange", shown);
    };
  }, [key]);
  return state;
}

/** The clock, ticking every `ms`, for "3m ago" texts. */
export function useNow(ms: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(id);
  }, [ms]);
  return now;
}

import { useCallback, useEffect, useRef, useState } from "react";
import * as api from "../data/api";
import type { T } from "../i18n";

// Dictation (Settings › Voice, voice.go): the bar's microphone records
// what the user says, eriphe-core has it written by the service the user
// chose, and the words join the draft. Nothing is sent: the user reads
// and sends, as if typed. One tap starts, one tap stops; Escape cancels.
// Nothing listens without that tap, and the microphone is let go as soon
// as the recording stops.

export type Dictating = "idle" | "asking" | "listening" | "writing";

// TYPES are the recordings the services read, the browser's first: Chrome
// and Firefox record WebM, Safari MP4.
export const TYPES = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"];

// MAX_SECONDS ends a dictation that was forgotten: eriphe-core takes a few
// minutes of speech.
export const MAX_SECONDS = 300;

// pickType is the first recording type the browser makes; "" lets it
// choose.
export function pickType(supported: (type: string) => boolean): string {
  return TYPES.find((t) => supported(t)) ?? "";
}

// withWords adds what was said to the draft, after what is typed.
export function withWords(draft: string, words: string): string {
  const w = words.trim();
  if (!w) return draft;
  const d = draft.replace(/\s+$/, "");
  return d ? `${d} ${w}` : w;
}

export function canRecord(): boolean {
  return typeof navigator !== "undefined" && !!navigator.mediaDevices?.getUserMedia && typeof MediaRecorder !== "undefined";
}

// refused tells a microphone the user or the page refused from one the
// browser does not have.
export function refused(e: unknown): boolean {
  const name = (e as { name?: string } | null)?.name;
  return name === "NotAllowedError" || name === "SecurityError";
}

export type Dictation = {
  state: Dictating;
  seconds: number;
  note: string; // why the last one gave nothing, in the user's words
  start: () => void;
  stop: () => void;
  cancel: () => void;
};

export function useDictation(t: T, onWords: (words: string) => void): Dictation {
  const [state, setState] = useState<Dictating>("idle");
  const [seconds, setSeconds] = useState(0);
  const [note, setNote] = useState("");
  const rec = useRef<MediaRecorder | null>(null);
  const dropped = useRef(false);
  const words = useRef(onWords);
  words.current = onWords;

  const stop = useCallback(() => {
    if (rec.current?.state === "recording") rec.current.stop();
  }, []);
  const cancel = useCallback(() => {
    dropped.current = true;
    stop();
  }, [stop]);

  const start = useCallback(async () => {
    if (rec.current) return;
    setNote("");
    if (!canRecord()) {
      setNote(t("espace.mic.unsupported"));
      return;
    }
    setState("asking");
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch (e) {
      setState("idle");
      setNote(t(refused(e) ? "espace.mic.denied" : "espace.mic.unsupported"));
      return;
    }
    const type = pickType((x) => MediaRecorder.isTypeSupported(x));
    const r = new MediaRecorder(stream, type ? { mimeType: type } : undefined);
    const chunks: Blob[] = [];
    dropped.current = false;
    r.ondataavailable = (e) => {
      if (e.data.size > 0) chunks.push(e.data);
    };
    r.onstop = async () => {
      stream.getTracks().forEach((tr) => tr.stop()); // the microphone is let go
      rec.current = null;
      setSeconds(0);
      const audio = new Blob(chunks, { type: r.mimeType || type });
      if (dropped.current) {
        setState("idle");
        return;
      }
      if (!audio.size) {
        setState("idle");
        setNote(t("espace.mic.empty"));
        return;
      }
      setState("writing");
      const got = await api.transcribe(audio, audio.type).catch((): api.Transcribed => ({ error: "" }));
      setState("idle");
      if ("text" in got) {
        if (got.text.trim()) words.current(got.text);
        else setNote(t("espace.mic.empty"));
      } else {
        setNote(got.error || t("espace.mic.failed"));
      }
    };
    rec.current = r;
    r.start();
    setSeconds(0);
    setState("listening");
  }, [t]);

  // The time spoken, and the end of a dictation left on.
  useEffect(() => {
    if (state !== "listening") return;
    const timer = setInterval(() => setSeconds((s) => s + 1), 1000);
    return () => clearInterval(timer);
  }, [state]);
  useEffect(() => {
    if (seconds >= MAX_SECONDS) stop();
  }, [seconds, stop]);

  // A bar that goes lets the microphone go.
  useEffect(() => cancel, [cancel]);

  return { state, seconds, note, start: () => void start(), stop, cancel };
}

// clock is the time spoken, m:ss.
export function clock(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

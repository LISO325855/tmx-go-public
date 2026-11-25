"""The speech-to-text worker (voice.py starts it): a separate process so the daemon never loads CUDA, and
quitting it frees the GPU memory. It reads one JSON request per line on stdin and answers one JSON line
on stdout:

    {"audio": PATH, "initial_prompt": "...", "hotwords": "...", "language": null}
    -> {"ok": true, "text": "...", "language": "fr", "duration": 12.3}  or  {"ok": false, "error": "..."}
Long recordings (over CHUNK_S) first answer {"progress": S, "duration": S} once per chunk, then the result.

The model loads at the first request. Audio is decoded HERE to 16 kHz mono (load_audio): WAV directly, anything
else (webm/opus, mp4/AAC, ogg) through ffmpeg when the machine has it; only without ffmpeg does faster-whisper's
PyAV decoding run (tmx-lead, 10 Oct: PyAV 19 broke it on Clara's Mac; `tmx voice install` pins av<19).
TMX_VOICE_DEVICE / TMX_VOICE_COMPUTE may be "auto": CUDA when CTranslate2 sees a GPU, else CPU/int8; on CUDA the
compute type auto-picks the best of float16/int8_float16/int8 that ctranslate2.get_supported_compute_types
actually lists for that device (a GPU without fast float16, e.g. Forge's Pascal Quadro P4000, lands on int8
instead of a float16 that would not work there). Environment (from voice.py): TMX_VOICE_MODEL, TMX_VOICE_FALLBACK (tried if the model fails to load), TMX_VOICE_DEVICE,
TMX_VOICE_COMPUTE, TMX_VOICE_TOOLS (folder put first on sys.path), TMX_VOICE_DLL_DIRS (os.pathsep-separated; CUDA/cuDNN DLLs on Windows).
"""
from __future__ import annotations

import json
import os
import sys


def verify_pinned() -> None:
    """DLL folders inside the tools (TMX_VOICE_PINNED: cuda, ctranslate2) may hold only the DLLs recorded in
    <tools>/manifest.json, byte for byte (tmx-sec V6). Raises with the reason; nothing is loaded before this."""
    tools = os.environ.get("TMX_VOICE_TOOLS")
    subs = [s for s in os.environ.get("TMX_VOICE_PINNED", "").split(os.pathsep) if s]
    if not tools or not subs:
        return
    present = {f"{s}/{n}": os.path.join(tools, s, n) for s in subs if os.path.isdir(os.path.join(tools, s))
               for n in os.listdir(os.path.join(tools, s)) if n.lower().endswith(".dll")}
    if not present:
        return
    try:
        with open(os.path.join(tools, "manifest.json"), encoding="utf-8") as f:
            pinned = json.load(f)["files"]
    except (OSError, ValueError, KeyError):
        raise RuntimeError("the GPU libraries in the voice tools are not pinned: run python -m tmx.voice pin") from None
    import hashlib
    for rel, path in sorted(present.items()):
        want = (pinned.get(rel) or {}).get("sha256")
        if not want:
            raise RuntimeError(f"unexpected DLL in the voice tools: {rel} (not in manifest.json)")
        h = hashlib.sha256()
        with open(path, "rb") as f:
            for block in iter(lambda: f.read(1 << 20), b""):
                h.update(block)
        if h.hexdigest() != want:
            raise RuntimeError(f"a DLL in the voice tools changed since it was pinned: {rel}")


def _dll_dirs() -> list:
    verify_pinned()
    keep = []
    if hasattr(os, "add_dll_directory"):
        for d in filter(None, os.environ.get("TMX_VOICE_DLL_DIRS", "").split(os.pathsep)):
            if os.path.isdir(d):
                keep.append(os.add_dll_directory(d))
                os.environ["PATH"] = d + os.pathsep + os.environ.get("PATH", "")
    return keep


SAMPLE_RATE = 16000


def device_and_compute() -> tuple[str, str]:
    """TMX_VOICE_DEVICE/COMPUTE, with "auto" resolved: a Mac or a GPU-less PC must not fail on a cuda default."""
    dev = os.environ.get("TMX_VOICE_DEVICE", "auto") or "auto"
    comp = os.environ.get("TMX_VOICE_COMPUTE", "auto") or "auto"
    if dev == "auto":
        try:
            import ctranslate2
            dev = "cuda" if ctranslate2.get_cuda_device_count() > 0 else "cpu"
        except Exception:  # noqa: BLE001 - no GPU support at all: the CPU
            dev = "cpu"
    if comp == "auto":
        comp = "int8"  # works on the CPU and on every CUDA device CTranslate2 supports
        if dev == "cuda":
            try:
                import ctranslate2
                supported = set(ctranslate2.get_supported_compute_types(dev, device_index=0))
            except Exception:  # noqa: BLE001 - unsupported/unintrospectable: the choice above stands
                supported = set()
            for candidate in ("float16", "int8_float16", "int8"):
                if candidate in supported:
                    comp = candidate
                    break
    return dev, comp


def load_audio(path: str):
    """The recording as 16 kHz mono float32 (numpy).
    WAV: read directly. Other formats: ffmpeg when present (no PyAV, whose 19.x API broke faster-whisper)."""
    try:
        import numpy as np
    except ImportError:          # no numpy next to faster-whisper (only fake test tools): its own decoding
        return path
    with open(path, "rb") as f:
        head = f.read(12)
    if head[:4] == b"RIFF" and head[8:12] == b"WAVE":
        import wave
        with wave.open(path, "rb") as w:
            ch, width, rate = w.getnchannels(), w.getsampwidth(), w.getframerate()
            raw = w.readframes(w.getnframes())
        if width != 2:
            raise ValueError(f"WAV with {8 * width}-bit samples: only 16-bit PCM is read here")
        a = np.frombuffer(raw, dtype="<i2").astype(np.float32) / 32768.0
        if ch > 1:
            a = a.reshape(-1, ch).mean(axis=1)
        if rate != SAMPLE_RATE and len(a):
            n = int(round(len(a) * SAMPLE_RATE / rate))
            a = np.interp(np.linspace(0, len(a) - 1, n), np.arange(len(a)), a).astype(np.float32)
        return a
    import shutil
    import subprocess
    ff = os.environ["TMX_VOICE_FFMPEG"] if "TMX_VOICE_FFMPEG" in os.environ else shutil.which("ffmpeg")
    if ff and not os.path.isabs(ff):
        ff = None                                    # only the absolute path the daemon resolved
    if not ff:
        # Decode BEFORE the energy check. Passing a filename to Whisper bypassed
        # that check on machines without ffmpeg, admitting silent phone recordings.
        from faster_whisper.audio import decode_audio
        return decode_audio(path, sampling_rate=SAMPLE_RATE)
    p = subprocess.run([ff, "-nostdin", "-v", "error", "-i", path, "-f", "s16le", "-ac", "1", "-ar", str(SAMPLE_RATE), "-"],
                       capture_output=True, timeout=120, creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
    if p.returncode != 0:
        raise ValueError(f"ffmpeg could not decode the recording: {p.stderr.decode(errors='replace')[-200:]}")
    return np.frombuffer(p.stdout, dtype="<i2").astype(np.float32) / 32768.0


SILENCE_RMS = 0.003
MIN_AUDIO_S = 0.5
RETRY_MESSAGE = "Couldn't hear you, please retry. Check the microphone on this device."


def audio_levels(audio):
    """Duration, RMS, peak and active seconds; no transcript or audio is logged."""
    if isinstance(audio, str):
        # Only the dependency-free fake model uses this path: real Whisper imports numpy.
        return None
    import math
    # Also usable with plain sequences in dependency-free tests.
    a = audio
    if not all(math.isfinite(x) for x in a):
        raise ValueError("invalid audio samples")
    rms_of = lambda values: math.sqrt(math.fsum(float(x) ** 2 for x in values) / len(values)) if len(values) else 0.0
    rms = rms_of(a)
    peak = float(max(map(abs, a), default=0.0))
    # A single mic pop must not make a mostly silent clip count as clear speech.
    window = SAMPLE_RATE // 10
    active = sum(len(a[i:i + window]) for i in range(0, len(a), window)
                 if rms_of(a[i:i + window]) >= SILENCE_RMS) / SAMPLE_RATE
    return {"duration": len(a) / SAMPLE_RATE, "rms": rms, "peak": peak, "active": active}


def unusable_audio(levels) -> bool:
    return levels is not None and (levels["duration"] < MIN_AUDIO_S or levels["rms"] < SILENCE_RMS
                                   or levels["peak"] < SILENCE_RMS)


LOW_ENERGY_PHRASES = {"thank you", "thanks", "you", "thanks for watching", "thank you for watching"}


def low_energy_hallucination(text: str, levels) -> bool:
    import re
    import unicodedata
    normalized = " ".join(re.findall(r"\w+", unicodedata.normalize("NFKC", text).casefold()))
    low = levels is not None and (levels["rms"] < 0.01 or levels["active"] < MIN_AUDIO_S)
    return bool(low and (normalized in LOW_ENERGY_PHRASES or matched_hallucination_phrase(text)))


def is_silent(audio) -> bool:
    """True when the whole clip's RMS is below SILENCE_RMS: skip the model (26 Oct, VAD's old job, now off)."""
    if isinstance(audio, str):          # a path (PyAV decodes it): can't check RMS here, let the model run
        return False
    import numpy as np
    if not len(audio):
        return True
    return float(np.sqrt(np.mean(np.square(audio)))) < SILENCE_RMS


def is_hallucination(segment) -> bool:
    """The usual faster-whisper filter for text the model invents on near-silence (26 Oct, VAD's old job).
    OR, not AND (27 Oct: a near-silent/accidental recording was transcribed as "Closed Captioning provided
    by www.patreon.com.au" and sent in the owner's name - the model was CONFIDENT about that invented caption
    text, so avg_logprob never dropped below -1.0 even though no_speech_prob correctly flagged it as
    probably not speech. Either signal alone is real evidence of an invented segment.

    tmx-lead, 27 Oct: this flags a segment as SUSPECT, but a flagged segment's text is never silently
    dropped from the transcript any more - see hold_reason(). A sentence with one weak segment costs
    the owner one "Send anyway?" tap, never a missing clause."""
    return getattr(segment, "no_speech_prob", 0) > 0.6 or getattr(segment, "avg_logprob", 0) < -1.0


# Case-insensitive substrings (27 Oct, same incident): the common video/subtitle boilerplate faster-whisper
# invents on near-silence - confidently, so is_hallucination() alone does not always catch it. Matched
# against the FINAL joined text, not each segment, since the real phrase can straddle a segment boundary.
# Never logged beyond the pattern's own index/name (tmx-sec: no transcript content in the logs).
HALLUCINATION_PHRASES = (
    "closed captioning provided by",
    "thanks for watching",
    "thank you for watching",
    "subtitles by",
    "amara.org",
    "untertitel im auftrag des zdf",
    "untertitel der amara",
    "sous-titrage",
    "sous-titres réalisés",
    "please subscribe",
    "www.patreon.com",
)


def matched_hallucination_phrase(text: str) -> str | None:
    """The first blocklisted phrase found in `text` (case-insensitive substring), or None."""
    low = text.lower()
    for phrase in HALLUCINATION_PHRASES:
        if phrase in low:
            return phrase
    return None


# tmx-lead, 27 Oct: don't hold every short "Yes"/"No" - a clear, confident one-word answer must pass.
# Held for shortness only if total speech < VERY_SHORT_S, OR (< SHORT_S AND no_speech_prob > SHORT_UNSURE_PROB
# on one of those short segments): a click or a breath is held, a confident short answer is not.
VERY_SHORT_S = 0.6
SHORT_S = 1.0
SHORT_UNSURE_PROB = 0.5


def speech_duration(segments) -> float:
    """Total seconds covered by these segments (end - start summed) - a near-silent/accidental recording
    rarely has a full second of anything the model marks as a segment at all."""
    return sum(max(0.0, float(getattr(s, "end", 0)) - float(getattr(s, "start", 0))) for s in segments)


def hold_reason(text: str, segments) -> str | None:
    """Why a transcript should be held for "Send anyway?" instead of sent automatically, or None to send it
    as usual. Any one of these alone holds the WHOLE transcript (27 Oct: never silently drop a flagged
    segment's words - held in full, or sent in full): a suspect segment (is_hallucination), a blocklisted
    phrase, or too little/too unsure speech. Returns a short, loggable reason name - never the transcript
    text itself."""
    if not text.strip():
        return None                   # nothing to hold: the empty-text path already sends nothing
    if any(is_hallucination(s) for s in segments):
        return "suspect"
    phrase = matched_hallucination_phrase(text)
    if phrase:
        return f"pattern:{HALLUCINATION_PHRASES.index(phrase)}"
    speech_s = speech_duration(segments)
    if speech_s < VERY_SHORT_S:
        return "short"
    if speech_s < SHORT_S and any(getattr(s, "no_speech_prob", 0) > SHORT_UNSURE_PROB for s in segments):
        return "short"
    return None


CHUNK_S, OVERLAP_S = 570.0, 15.0


class _Info:
    def __init__(self, language, duration):
        self.language, self.duration = language, duration


def transcribe_chunked(model, audio, kw: dict, progress=None, chunk_s: float = CHUNK_S, overlap_s: float = OVERLAP_S):
    """(segments, info) for a recording of any length. Short audio (or a path, when there is no numpy to
    decode it): one model.transcribe call, as before. Longer: chunk_s windows overlapping by overlap_s; a
    segment that ends inside what the previous window already covered is dropped (the overlap dedupe), and
    segment times are shifted to the whole recording. progress(done_s, total_s) after every window."""
    total = len(audio) / SAMPLE_RATE if hasattr(audio, "__len__") and not isinstance(audio, str) else 0.0
    if total <= chunk_s + 1:
        segments, info = model.transcribe(audio, **kw)
        return list(segments), info
    out, pos, last_end, language = [], 0.0, 0.0, None
    while True:
        window = audio[int(pos * SAMPLE_RATE): int((pos + chunk_s) * SAMPLE_RATE)]
        segs, info = model.transcribe(window, **kw)
        language = language or info.language
        for s in segs:
            if pos + s.end <= last_end + 0.5:
                continue
            try:
                s = s._replace(start=s.start + pos, end=s.end + pos)
            except AttributeError:      # not a namedtuple (a test double): shift in place
                s.start, s.end = s.start + pos, s.end + pos
            out.append(s)
            last_end = max(last_end, s.end)
        done = min(total, pos + chunk_s)
        if progress:
            progress(done, total)
        if pos + chunk_s >= total:
            break
        pos += chunk_s - overlap_s
    return out, _Info(language, total)


def tone_wav(path: str, seconds: float = 1.0) -> str:
    """A quiet 440 Hz tone as 16 kHz 16-bit WAV: the real-transcription check's sample (made here, nothing bundled)."""
    import math
    import struct
    import wave
    n = int(SAMPLE_RATE * seconds)
    frames = b"".join(struct.pack("<h", int(1600 * math.sin(2 * math.pi * 440 * i / SAMPLE_RATE))) for i in range(n))
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SAMPLE_RATE)
        w.writeframes(frames)
    return path


def check(transcribe: bool = False) -> dict:
    """--check (voice.check, for doctor): the packages import, and with cuda the GPU and its libraries load.
    --transcribe: also a REAL transcription of a 1 s tone on the CPU with the small model, so "ok" means it works
    (tmx-lead, 10 Oct: the import check said ok on a Mac whose decoding was broken)."""
    out = {"ok": False}
    try:
        import ctranslate2
        import faster_whisper
        out.update(faster_whisper=getattr(faster_whisper, "__version__", "?"), ctranslate2=ctranslate2.__version__)
        dev, _ = device_and_compute()
        out["device"] = dev
        if dev == "cuda":
            out["cuda"] = ctranslate2.get_cuda_device_count()
            if not out["cuda"]:
                raise RuntimeError("no CUDA device seen by CTranslate2")
            if os.name == "nt":
                import ctypes
                for dll in ("cublas64_12.dll", "cublasLt64_12.dll", "cudnn64_9.dll"):
                    ctypes.WinDLL(dll)                   # raises when the GPU library cannot be found
        if transcribe:
            small = os.environ.get("TMX_VOICE_FALLBACK") or os.environ.get("TMX_VOICE_MODEL")
            if not small or not os.path.isdir(small):
                raise RuntimeError("no speech model on disk for the real check")
            import tempfile
            from faster_whisper import WhisperModel
            m = WhisperModel(small, device="cpu", compute_type="int8")
            with tempfile.TemporaryDirectory() as d:
                sample = tone_wav(os.path.join(d, "check.wav"))
                segments, info = m.transcribe(load_audio(sample), language="en", beam_size=1, vad_filter=False)
                list(segments)                           # decoding runs lazily: force it
            out["transcribed"] = round(info.duration, 2)
        out["ok"] = True
    except Exception as exc:  # noqa: BLE001 - reported to doctor
        out["error"] = f"{type(exc).__name__}: {exc}"[:400]
    return out


def main() -> int:
    sys.stdin.reconfigure(encoding="utf-8")
    sys.stdout.reconfigure(encoding="utf-8")
    try:
        _handles = _dll_dirs()   # noqa: F841 (must stay alive)
    except RuntimeError as exc:   # the pinned DLLs do not match: refuse every request with the reason
        err = {"ok": False, "error": str(exc)}
        if "--check" in sys.argv[1:]:
            print(json.dumps(err), flush=True)
            return 0
        for line in sys.stdin:
            if line.strip():
                print(json.dumps(err), flush=True)
        return 0
    tools = os.environ.get("TMX_VOICE_TOOLS")
    if tools:                                        # a pip --target folder with faster-whisper and its DLLs
        sys.path.insert(0, tools)
    sys.stdin.reconfigure(encoding="utf-8")
    sys.stdout.reconfigure(encoding="utf-8")
    if "--check" in sys.argv[1:]:
        print(json.dumps(check("--transcribe" in sys.argv[1:])), flush=True)
        return 0
    os.environ.setdefault("HF_HUB_DISABLE_SYMLINKS_WARNING", "1")
    os.environ.setdefault("HF_HUB_OFFLINE", "1")     # never download from here: the model is configured on disk
    model, used = None, None
    out = sys.stdout
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
            if model is None:
                from faster_whisper import WhisperModel
                fallback = os.environ.get("TMX_VOICE_FALLBACK")
                for name in (os.environ["TMX_VOICE_MODEL"], fallback):
                    if not name or not os.path.isdir(name):
                        continue
                    try:
                        dev, comp = device_and_compute()
                        model = WhisperModel(name, device=dev, compute_type=comp)
                        used = os.path.basename(os.path.normpath(name))
                        break
                    except Exception:  # noqa: BLE001 - the fallback model gets its turn
                        if name == fallback:
                            raise
                if model is None:
                    raise RuntimeError("no speech model could be loaded")
            if req.get("warm"):
                # tmx-go only: the daemon asks this once at its start, so that the model is in
                # memory before the first dictation instead of loading during it.
                out.write(json.dumps({"ok": True, "text": "", "warm": True, "device": device_and_compute()[0],
                                      "model": used}) + "\n")
                out.flush()
                continue
            kw = {"language": req.get("language") or None, "task": "transcribe", "beam_size": 5,
                  # vad_filter False (26 Oct): on the owner's phone notes it dropped whole clauses (a 5.7 s clip
                  # lost its first sentence, a 25.5 s clip lost two mid-sentence clauses); the silence guard
                  # and hallucination filter below take over VAD's old job of not transcribing silence.
                  "vad_filter": False, "condition_on_previous_text": False,
                  "initial_prompt": req.get("initial_prompt") or None}
            if req.get("hotwords"):
                kw["hotwords"] = req["hotwords"]
            audio = load_audio(req["audio"])
            levels = audio_levels(audio)
            if unusable_audio(levels):
                res = {"ok": True, "text": "", "language": None, "duration": round(levels["duration"], 2),
                       "device": device_and_compute()[0], "model": used}
            else:
                # 2 Nov: no length limit any more (an 18-min client call failed on the old 600 s max_s);
                # long recordings are transcribed in overlapping chunks, with a progress line per chunk.
                segments, info = transcribe_chunked(model, audio, kw, progress=lambda done, total: print(
                    json.dumps({"progress": round(done, 1), "duration": round(total, 1)}), file=out, flush=True))
                text = " ".join(s.text.strip() for s in segments).strip()
                if low_energy_hallucination(text, levels):
                    text = ""
                reason = hold_reason(text, segments)
                res = {"ok": True, "text": text, "language": info.language, "duration": round(info.duration, 2),
                       "device": device_and_compute()[0], "model": used}
                # tmx-go only (not in Python tmx's copy): the segments with their times, which Go stores
                # for recordings. Besides this and the "warm" request above, nothing in this file
                # differs from Python tmx's voice_worker.py.
                res["segments"] = [{"start": round(float(s.start), 3), "end": round(float(s.end), 3),
                                    "text": s.text.strip()} for s in segments] if text else []
                if reason:
                    res["held"] = True
                    res["held_reason"] = reason
                    print(f"voice: held a transcript ({reason})", file=sys.stderr, flush=True)
            if not res.get("text"):
                res["message"] = RETRY_MESSAGE
        except Exception as exc:  # one bad file never ends the worker
            res = {"ok": False, "error": f"{type(exc).__name__}: {exc}"[:500]}
        out.write(json.dumps(res, ensure_ascii=False) + "\n")
        out.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())

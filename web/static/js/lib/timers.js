// Pure timer logic for cook mode (spec 70 section 5.2). State is an array of plain objects with absolute
// timestamps so it survives reloads: {id, recipeId, label, endsAt, preAlertAt, noticeAt, noticeText,
// startedAt, firedPre, firedNotice, firedEnd}
export const STORAGE_KEY = 'nwr_timers';
export const PRE_ALERT_MS = 5 * 60 * 1000;
export const PRE_ALERT_MIN_SECONDS = 10 * 60;
export const MAX_AGE_MS = 12 * 60 * 60 * 1000;
export const REPEAT_EVERY_MS = 10 * 1000;
export const REPEAT_FOR_MS = 2 * 60 * 1000;

export function makeTimer({ id, recipeId, label, seconds, now, preAlert = false, stage1Seconds = 0, noticeText = '' }) {
  const endsAt = now + seconds * 1000;
  const timer = {
    id, recipeId, label, startedAt: now, endsAt,
    preAlertAt: preAlert && seconds >= PRE_ALERT_MIN_SECONDS ? endsAt - PRE_ALERT_MS : null,
    noticeAt: null, noticeText: '',
    firedPre: false, firedNotice: false, firedEnd: false,
  };
  if (noticeText && stage1Seconds > 0 && stage1Seconds < seconds) {
    timer.noticeAt = now + stage1Seconds * 1000;
    timer.noticeText = noticeText;
  }
  return timer;
}

export function remainingMs(timer, now) { return Math.max(0, timer.endsAt - now); }

export function formatRemaining(ms) {
  const total = Math.ceil(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n) => String(n).padStart(2, '0');
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

// Returns new timer list plus events [{type:'pre'|'notice'|'end', timer}] for alerts that became due.
export function tick(timers, now) {
  const events = [];
  const next = timers.map((t) => {
    const u = { ...t };
    if (!u.firedPre && u.preAlertAt != null && now >= u.preAlertAt && now < u.endsAt) {
      u.firedPre = true; events.push({ type: 'pre', timer: u });
    } else if (!u.firedPre && u.preAlertAt != null && now >= u.endsAt) {
      u.firedPre = true; // already past the end: only the final alert matters
    }
    if (!u.firedNotice && u.noticeAt != null && now >= u.noticeAt && now < u.endsAt) {
      u.firedNotice = true; events.push({ type: 'notice', timer: u });
    } else if (!u.firedNotice && u.noticeAt != null && now >= u.endsAt) {
      u.firedNotice = true;
    }
    if (!u.firedEnd && now >= u.endsAt) {
      u.firedEnd = true; u.firedAt = now; events.push({ type: 'end', timer: u });
    }
    return u;
  });
  return { timers: next, events };
}

export function prune(timers, now) {
  return timers.filter((t) => now - t.endsAt < MAX_AGE_MS && now - t.startedAt < MAX_AGE_MS + 24 * 3600 * 1000);
}

export function shouldRepeatChime(firedAt, lastChimeAt, now) {
  if (firedAt == null) return false;
  if (now - firedAt > REPEAT_FOR_MS) return false;
  return now - lastChimeAt >= REPEAT_EVERY_MS;
}

export function removeTimer(timers, id) { return timers.filter((t) => t.id !== id); }

export function load(storage, now) {
  try {
    const raw = storage.getItem(STORAGE_KEY);
    const arr = raw ? JSON.parse(raw) : [];
    if (!Array.isArray(arr)) return [];
    return prune(arr.filter((t) => t && typeof t.endsAt === 'number' && typeof t.id === 'string'), now);
  } catch { return []; }
}

export function save(storage, timers) {
  try { storage.setItem(STORAGE_KEY, JSON.stringify(timers)); return true; } catch { return false; }
}

let counter = 0;
export function newId(now) { counter += 1; return `t${now.toString(36)}${counter}`; }

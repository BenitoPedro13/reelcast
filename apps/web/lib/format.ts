export function formatDuration(durationSec: string | null): string | null {
  if (durationSec === null) return null;
  const totalSeconds = Math.round(Number(durationSec));
  if (!Number.isFinite(totalSeconds)) return null;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
}

/** Formatting utilities. */

export function formatTimestamp(iso: string | undefined): string {
  if (!iso) return "—";
  try {
    const d = new Date(iso);
    return d.toLocaleString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}

export function formatRelativeTime(iso: string | undefined): string {
  if (!iso) return "—";
  try {
    const d = new Date(iso);
    const now = Date.now();
    const diff = now - d.getTime();

    if (diff < 0) return "just now";
    if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`;
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
    return `${Math.floor(diff / 86_400_000)}d ago`;
  } catch {
    return iso;
  }
}

export function shortSha(sha: string | undefined): string {
  if (!sha) return "—";
  return sha.slice(0, 8);
}

export function statusColor(status: string): string {
  switch (status) {
    case "success":
      return "badge-success";
    case "error":
      return "badge-error";
    case "running":
      return "badge-info";
    default:
      return "badge-neutral";
  }
}

export function levelPrefix(level: string | undefined): string {
  switch (level?.toUpperCase()) {
    case "ERROR":
      return "ERR";
    case "WARN":
      return "WRN";
    case "INFO":
      return "INF";
    case "DEBUG":
      return "DBG";
    default:
      return "???";
  }
}

export function levelColor(level: string | undefined): string {
  switch (level?.toUpperCase()) {
    case "ERROR":
      return "text-error";
    case "WARN":
      return "text-warning";
    case "INFO":
      return "text-info";
    case "DEBUG":
      return "text-neutral";
    default:
      return "";
  }
}

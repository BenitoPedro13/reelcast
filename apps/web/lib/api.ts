const API_URL = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:3001';

export type VideoStatus =
  | 'uploading'
  | 'queued'
  | 'processing'
  | 'ready'
  | 'failed';

export interface Rendition {
  id: string;
  videoId: string;
  height: number;
  bitrateKbps: number;
  playlistKey: string;
}

export interface Video {
  id: string;
  title: string;
  description: string | null;
  status: VideoStatus;
  sourceKey: string;
  durationSec: string | null;
  masterManifestKey: string | null;
  thumbnailKey: string | null;
  masterManifestUrl: string | null;
  thumbnailUrl: string | null;
  failureReason: string | null;
  createdAt: string;
}

export interface VideoWithRenditions extends Video {
  renditions: Rendition[];
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    cache: 'no-store',
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  });

  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new ApiError(
      res.status,
      `${init?.method ?? 'GET'} ${path} failed: ${res.status} ${body}`,
    );
  }

  return res.json() as Promise<T>;
}

export function createVideo(input: {
  title: string;
  description?: string;
}): Promise<{ id: string; uploadUrl: string }> {
  return apiFetch('/videos', { method: 'POST', body: JSON.stringify(input) });
}

export function completeVideo(id: string): Promise<Video> {
  return apiFetch(`/videos/${id}/complete`, { method: 'POST' });
}

export async function getVideo(
  id: string,
): Promise<VideoWithRenditions | null> {
  try {
    return await apiFetch<VideoWithRenditions>(`/videos/${id}`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export function listVideos(status?: VideoStatus): Promise<Video[]> {
  const qs = status ? `?status=${status}` : '';
  return apiFetch(`/videos${qs}`);
}

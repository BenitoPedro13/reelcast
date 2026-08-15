import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { listVideos } from "@/lib/api";
import { formatDuration } from "@/lib/format";

export default async function BrowsePage() {
  const videos = await listVideos("ready");

  if (videos.length === 0) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col items-center gap-4 px-4 py-24 text-center">
        <h1 className="text-2xl font-semibold">No videos yet</h1>
        <p className="text-muted-foreground">
          Upload one to see it show up here once it&apos;s ready.
        </p>
        <Button render={<Link href="/upload" />}>Upload a video</Button>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-8">
      <div className="grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-3">
        {videos.map((video) => {
          const duration = formatDuration(video.durationSec);
          return (
            <Link
              key={video.id}
              href={`/watch/${video.id}`}
              className="group overflow-hidden rounded-lg border"
            >
              <div className="relative aspect-video bg-muted">
                {video.thumbnailUrl ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img
                    src={video.thumbnailUrl}
                    alt=""
                    className="h-full w-full object-cover"
                  />
                ) : null}
                {duration ? (
                  <Badge className="absolute bottom-2 right-2">
                    {duration}
                  </Badge>
                ) : null}
              </div>
              <div className="p-3">
                <h2 className="line-clamp-1 font-medium group-hover:underline">
                  {video.title}
                </h2>
                {video.description ? (
                  <p className="line-clamp-2 text-sm text-muted-foreground">
                    {video.description}
                  </p>
                ) : null}
              </div>
            </Link>
          );
        })}
      </div>
    </div>
  );
}

import { notFound } from "next/navigation";
import { getVideo } from "@/lib/api";
import { formatDuration } from "@/lib/format";
import { WatchPlayerArea } from "@/components/watch-player-area";

export default async function WatchPage(props: PageProps<"/watch/[id]">) {
  const { id } = await props.params;
  const video = await getVideo(id);
  if (!video) notFound();

  const duration = formatDuration(video.durationSec);

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4 px-4 py-8">
      <div>
        <h1 className="text-2xl font-semibold">{video.title}</h1>
        {duration ? (
          <p className="text-sm text-muted-foreground">{duration}</p>
        ) : null}
      </div>
      <WatchPlayerArea video={video} />
      {video.description ? (
        <p className="text-sm whitespace-pre-wrap">{video.description}</p>
      ) : null}
    </div>
  );
}

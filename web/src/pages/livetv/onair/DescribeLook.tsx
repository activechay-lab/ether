import type { Style } from "./studio-model";
import { CreateTab } from "./editor/CreateTab";

/** Scene-level entry point shares the same streaming builder as Create. */
export function DescribeLook({
  look,
  onChange,
  sceneId,
}: {
  look: Style;
  onChange(style: Style): void;
  sceneId?: string;
}) {
  return <CreateTab style={look} onChange={onChange} selectedSceneId={sceneId} sceneOnly />;
}

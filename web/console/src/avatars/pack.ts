import animation from "../../../../assets/agent-avatars/animation.json" with { type: "json" };
import scene from "../../../../assets/agent-avatars/scene.json" with { type: "json" };
import type { AnimationSpec } from "../../../../assets/agent-avatars/player";
import type { FaceSpec, ScenePack } from "../../../../assets/agent-avatars/render";
import { findFace, renderFace, resolveFaceId } from "../../../../assets/agent-avatars/render.js";
import { attachIdle } from "../../../../assets/agent-avatars/player.js";

export const avatarScene = scene as ScenePack;
export const avatarAnimation = animation as AnimationSpec;

export { attachIdle, findFace, renderFace, resolveFaceId };

export function avatarById(id: string | null | undefined): FaceSpec | undefined {
  return findFace(avatarScene, id);
}

export const AVATAR_FACES: { id: string; label: string; src: string }[] = avatarScene.faces.map(
  (face) => ({
    id: face.id,
    label: face.label,
    src: `/avatars/${face.id}.svg`,
  }),
);

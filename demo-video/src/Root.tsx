import {Composition} from "remotion";

import {BridgeSafeDemo} from "./BridgeSafeDemo";
import {DURATION_IN_FRAMES, FPS, HEIGHT, WIDTH} from "./story";

export const RemotionRoot = () => (
  <Composition
    id="BridgeSafeDemo"
    component={BridgeSafeDemo}
    durationInFrames={DURATION_IN_FRAMES}
    fps={FPS}
    width={WIDTH}
    height={HEIGHT}
  />
);

# AgentIcon

AgentIcon is the shared agent mark: one figure, one role badge, and one palette ink colour. It is a presentational composite. It does not load an agent, read a store, or turn an old colour into a palette colour.

## Contract

| Input | Meaning |
|---|---|
| `figure` | `Robot`, `Man`, `Woman`, or `Omnipus`. Omnipus is the product default. |
| `role` | One of the 31 role slugs. The badge is drawn at every size. An unknown slug is a type error, not a fallback glyph. |
| `color` | One of the ten palette hexes. Ink is fully opaque (`currentColor`). |
| `size` | `18`, `26`, `40`, or `48` pixels. No other size. |
| `motion` | `none` (default), `thinking`, `working`, or `waiting`. The icon does not render the state phrase. |
| `decorative` | Default true: the mark is hidden from assistive technology because the row beside it already names the agent. |
| `name` | Required when `decorative` is false. It is the agent name, announced as the image name. |
| `reducedMotion` | Omitted follows the reduced-motion preference. `true` forces the still mark. `false` forces motion. |

Ink opacity stays 1. Thinking, working, and waiting move a glow layer behind the ink. Reduced motion runs no loop.

## Publication

Catalogued as a composite. Public exports are `AgentIcon` and the type `AgentIconProps`. The art module is internal. The vocabulary of labels and groups lives in `src/lib/agentIdentity.ts`, typed against the generated unions.

# AgentIcon

AgentIcon is the shared agent mark: one figure, one role badge, and one palette ink colour. It is a presentational composite. It does not load an agent, read a store, or turn an old colour into a palette colour.

## Contract

| Input | Meaning |
|---|---|
| `figure` | `Robot`, `Man`, `Woman`, `Omnipus`, or `Monogram`. Omnipus is the product default. `Monogram` draws the uppercased first letter or digit of the agent's name in the agent's colour; a first character that is not a letter or digit renders `?`. |
| `role` | One of the 31 role slugs. The badge is drawn at every size. An unknown slug is a type error, not a fallback glyph. |
| `color` | One of the ten palette hexes. Ink is fully opaque (`currentColor`). |
| `size` | `18`, `26`, `40`, or `48` pixels. No other size. |
| `motion` | `none` (default), `thinking`, `working`, or `waiting`. The icon does not render the state phrase. |
| `decorative` | Default true: the mark is hidden from assistive technology because the row beside it already names the agent. |
| `name` | Required for every render. It is the agent name: `Monogram` draws its initial from it, and a non-decorative mark announces it as the image name. |
| `reducedMotion` | Omitted follows the reduced-motion preference. `true` forces the still mark. `false` forces motion. |

Ink opacity stays 1. Thinking, working, and waiting move a glow layer behind the ink. Reduced motion runs no loop.

## Publication

Catalogued as a composite. Public exports are `AgentIcon` and the type `AgentIconProps`. The art module is internal. The vocabulary of labels and groups lives in `src/lib/agentIdentity.ts`, typed against the generated unions.

# Slider

Slider is the Radix-backed numeric range primitive. It preserves controlled and uncontrolled single values and ranges, horizontal and vertical track geometry, keyboard changes, native form identity and disabled state. It renders one operative thumb for every supplied value. Multi-thumb ranges require one distinct, non-empty `thumbLabels` entry per thumb; single thumbs continue to accept `aria-label` or visible-label identity through `aria-labelledby`. `aria-describedby` reaches every operative thumb. Disabled sliders retain their value and expose muted thumbs. Each thumb has a 44px coarse-pointer hit region without changing the visible 16px geometry.

In forced-colours mode, the track uses Canvas and the filled range uses Highlight. The system-colour pair stays distinct while the thumb retains its visible boundary and keyboard focus. Normal palette and geometry are unchanged.

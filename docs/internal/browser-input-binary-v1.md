# Dedicated browser input binary v1

The two existing dedicated data channels use the DCEP protocol name
`omnipus.input.v1`. Both peers must use this version. JSON gesture packets and
unknown protocols/versions fail closed; there is no input transport fallback.
Signaling, control, navigation, and status remain their existing JSON messages.

Packets contain `4f 42 49` (OBI), version byte `01`, a kind byte, then a
little-endian uint32 presence bitmap (9 header bytes). Kind values are 1
mouse_move, 2 mouse_down, 3 mouse_up, 4 wheel, 5 key_down, 6 key_up, 7 text.
Present fields follow in bit order:

| Bit | Field | Encoding |
| --- | --- | --- |
| 0 | x | float64 LE |
| 1 | y | float64 LE |
| 2 | capture_width | float64 LE |
| 3 | capture_height | float64 LE |
| 4 | button | UTF-8 |
| 5 | delta_x | float64 LE |
| 6 | delta_y | float64 LE |
| 7 | key | UTF-8 |
| 8 | code | UTF-8 |
| 9 | key_code | float64 LE |
| 10 | text | UTF-8 |
| 11 | modifiers | float64 LE |
| 12 | url | UTF-8 |
| 13 | capture_generation | float64 LE |
| 14 | capture_id | UTF-8 |
| 15 | input_epoch | float64 LE |
| 16 | control_epoch | float64 LE |
| 17 | reliable_seq | float64 LE |
| 18 | hover_seq | float64 LE |
| 19 | gesture_barrier | float64 LE |

Each UTF-8 field starts with its uint16 little-endian byte length. Absence is
preserved separately from zero or empty text. Numeric fields retain existing
fractional-coordinate and safe-integer semantics. The generated schema remains
authoritative for ranges, integer fields, enums, and Unicode character limits.

Reject unknown bitmap bits, unknown kinds, malformed UTF-8, nonfinite numbers,
truncated values, trailing bytes, and packets larger than 65536 bytes. Decoded
frames still pass the existing schema validator and all channel, ownership,
capture, sequence, barrier, and queue checks. Internal JSON remarshal remains
for schema validation: this change does not establish reduced backend JSON
processing or measured latency improvement.

Independent golden vector: text `a`, input epoch 1, control epoch 0, reliable
sequence 1, barrier 0 (no other fields):

```
4f4249010700840b00010061
000000000000f03f0000000000000000
000000000000f03f0000000000000000
```

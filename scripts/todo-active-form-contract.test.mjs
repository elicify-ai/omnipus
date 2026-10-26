import assert from "node:assert/strict";
import { test } from "node:test";

import { TaskUpdateRequest, Todo } from "../src/lib/api/generated/schemas.ts";

// R1/R3 use the existing item limit: 500 Unicode code points, not UTF-16 units.
const todo = (activeForm) => ({
  text: "Draft the Q4 summary",
  status: "in_progress",
  active_form: activeForm,
});

for (const [name, value, accepted] of [
  ["empty", "", true],
  ["one ASCII character", "a", true],
  ["499 ASCII characters", "a".repeat(499), true],
  ["500 ASCII characters", "a".repeat(500), true],
  ["501 ASCII characters", "a".repeat(501), false],
  ["500 BMP characters", "界".repeat(500), true],
  ["501 BMP characters", "界".repeat(501), false],
  ["250 supplementary characters", "\u{20000}".repeat(250), true],
  ["251 supplementary characters", "\u{20000}".repeat(251), true],
  ["499 supplementary characters", "\u{20000}".repeat(499), true],
  ["500 supplementary characters", "\u{20000}".repeat(500), true],
  ["501 supplementary characters", "\u{20000}".repeat(501), false],
]) {
  test(`Todo active_form: ${name}`, () => {
    const input = todo(value);
    const result = Todo.safeParse(input);
    assert.equal(result.success, accepted, `${name}: ${JSON.stringify(result)}`);
    if (accepted) {
      assert.deepEqual(result.data, input, "validation must preserve the full phrase");
    } else {
      assert.deepEqual(
        result.error.issues.map(({ path, code }) => ({ path, code })),
        [{ path: ["active_form"], code: "too_big" }],
      );
    }
  });
}

test("Todo active_form remains optional", () => {
  const input = { text: "Draft the Q4 summary", status: "pending" };
  assert.deepEqual(Todo.parse(input), input);
});

for (const [name, value] of [
  ["null", null],
  ["number", 42],
  ["boolean", true],
  ["array", ["Drafting"]],
  ["object", { text: "Drafting" }],
]) {
  test(`Todo active_form rejects ${name}`, () => {
    const result = Todo.safeParse(todo(value));
    assert.equal(result.success, false);
    assert.deepEqual(
      result.error.issues.map(({ path, code }) => ({ path, code })),
      [{ path: ["active_form"], code: "invalid_type" }],
    );
  });
}

test("TaskUpdateRequest preserves 500 supplementary active_form characters", () => {
  const input = { todos: [todo("\u{20000}".repeat(500))] };
  assert.deepEqual(TaskUpdateRequest.parse(input), input);
});

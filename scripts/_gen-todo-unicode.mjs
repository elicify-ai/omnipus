import fs from "node:fs";

import yaml from "js-yaml";

const [sourcePath, todoSchemaPath] = process.argv.slice(2);
if (!sourcePath || !todoSchemaPath) {
  throw new Error("usage: _gen-todo-unicode.mjs <generated-zod-source> <Todo.yaml>");
}

// JSON Schema maxLength and the Go todo validator count Unicode code points.
// Zod 3's string.max counts UTF-16 units instead. Correct this new field only;
// do not change validation behavior for unrelated existing string properties.
const schema = yaml.load(fs.readFileSync(todoSchemaPath, "utf8"));
const property = schema?.properties?.active_form;
const limit = property?.maxLength;
if (property?.type !== "string" || !Number.isSafeInteger(limit) || limit < 0) {
  throw new Error("Todo.active_form must declare a string maxLength");
}

const source = fs.readFileSync(sourcePath, "utf8");
const declaration = /^export const Todo(?::[^\n=]+)? = z\.object\(\{[\s\S]*?^\}\);/m;
const todo = source.match(declaration)?.[0];
if (!todo) {
  throw new Error("generated Todo object was not found; generator layout changed");
}
const field = new RegExp(`(active_form:\\s*)z\\.string\\(\\)\\.max\\(${limit}\\)`, "g");
let matches = 0;
const corrected = todo.replace(field, (_match, prefix) => {
  matches += 1;
  return `${prefix}z.string().superRefine((value, ctx) => {
    if (Array.from(value).length > ${limit}) {
      ctx.addIssue({
        code: z.ZodIssueCode.too_big,
        maximum: ${limit},
        type: "string",
        inclusive: true,
        exact: false,
      });
    }
  })`;
});
if (matches !== 1) {
  throw new Error(`expected one generated Todo.active_form maxLength, found ${matches}`);
}
fs.writeFileSync(sourcePath, source.replace(declaration, () => corrected));
console.log(`  Todo.active_form: enforce ${limit} Unicode code points`);

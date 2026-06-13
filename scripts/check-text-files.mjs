#!/usr/bin/env node
import { execFileSync } from "node:child_process";

const repository = process.cwd().replaceAll("\\", "/");
const git = (...args) =>
  execFileSync("git", ["-c", `safe.directory=${repository}`, ...args], {
    maxBuffer: 64 * 1024 * 1024,
  });
const paths = git("-c", "core.quotepath=false", "ls-files", "-z")
  .toString("utf8")
  .split("\0")
  .filter(Boolean);
const decoder = new TextDecoder("utf-8", { fatal: true });
const errors = [];

for (const path of paths) {
  const attribute = git("check-attr", "-z", "binary", "--", path)
    .toString("utf8")
    .split("\0")[2];
  if (attribute === "set") continue;

  const data = git("show", `:${path}`);
  if (data.includes(0)) continue;

  if (
    data.length >= 3 &&
    data[0] === 0xef &&
    data[1] === 0xbb &&
    data[2] === 0xbf
  ) {
    errors.push(`${path}: UTF-8 BOM is not allowed`);
  }

  let text;
  try {
    text = decoder.decode(data);
  } catch (error) {
    errors.push(`${path}: invalid UTF-8 (${error.message})`);
    continue;
  }

  const replacementCharacterIndex = text.indexOf("\uFFFD");
  if (replacementCharacterIndex !== -1) {
    const line = text.slice(0, replacementCharacterIndex).split("\n").length;
    errors.push(
      `${path}:${line}: Unicode replacement character (U+FFFD) is not allowed`,
    );
  }
}

if (errors.length > 0) {
  console.error("Text file validation failed:");
  for (const error of errors) console.error(`- ${error}`);
  process.exit(1);
}

console.log(
  `Validated ${paths.length} tracked files as UTF-8 without BOM or U+FFFD.`,
);

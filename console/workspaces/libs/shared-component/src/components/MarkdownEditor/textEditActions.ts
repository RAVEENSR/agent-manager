/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

// Named to avoid colliding with the DOM's global `Selection` type.
export interface TextSelection {
  text: string;
  start: number;
}

export interface EditResult {
  text: string;
  selectionStart: number;
  selectionEnd: number;
}

/** Wraps the selection with `prefix`/`suffix` (bold, italic); falls back to `placeholder`. */
export function wrapSelection(
  { text, start }: TextSelection,
  end: number,
  prefix: string,
  suffix: string,
  placeholder: string,
): EditResult {
  const selected = text.slice(start, end) || placeholder;
  const before = text.slice(0, start);
  const after = text.slice(end);
  const selectionStart = before.length + prefix.length;
  return {
    text: `${before}${prefix}${selected}${suffix}${after}`,
    selectionStart,
    selectionEnd: selectionStart + selected.length,
  };
}

/** Prefixes every line touched by the selection with `prefix` (heading, quote, list markers). */
export function prefixLines(
  { text, start }: TextSelection,
  end: number,
  prefix: string,
): EditResult {
  const lineStart = text.lastIndexOf("\n", start - 1) + 1;
  const nextBreak = text.indexOf("\n", end);
  const lineEnd = nextBreak === -1 ? text.length : nextBreak;
  const segment = text.slice(lineStart, lineEnd);
  const prefixed = segment
    .split("\n")
    .map((line) => `${prefix}${line}`)
    .join("\n");
  return {
    text: text.slice(0, lineStart) + prefixed + text.slice(lineEnd),
    selectionStart: start + prefix.length,
    selectionEnd: end + (prefixed.length - segment.length),
  };
}

export function insertLink({ text, start }: TextSelection, end: number): EditResult {
  const linkText = text.slice(start, end) || "link text";
  const before = text.slice(0, start);
  const after = text.slice(end);
  const selectionStart = before.length + linkText.length + 3; // "[" + linkText + "]("
  return {
    text: `${before}[${linkText}](url)${after}`,
    selectionStart,
    selectionEnd: selectionStart + "url".length,
  };
}

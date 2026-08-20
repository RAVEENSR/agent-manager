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

import { useRef, useState } from 'react';
import { Alert, Button } from '@wso2/oxygen-ui';
import { Upload as UploadIcon } from '@wso2/oxygen-ui-icons-react';

export interface ParsedEnvEntry {
  key: string;
  value: string;
}

/**
 * Parses `.env`-style file content into key/value pairs. Skips blank lines
 * and `#` comments, splits each line on the first `=`, and strips matching
 * surrounding quotes from the value.
 */
export function parseEnvFileContent(text: string): ParsedEnvEntry[] {
  const result: ParsedEnvEntry[] = [];
  const lines = text.split(/\r?\n/);

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;

    const equalsIdx = trimmed.indexOf('=');
    if (equalsIdx === -1) continue;

    const key = trimmed.slice(0, equalsIdx).trim();
    let value = trimmed.slice(equalsIdx + 1).trim();

    if (
      value.length >= 2 &&
      ((value.startsWith('"') && value.endsWith('"')) ||
        (value.startsWith("'") && value.endsWith("'")))
    ) {
      value = value.slice(1, -1);
    }

    if (key) {
      result.push({ key, value });
    }
  }

  return result;
}

const MAX_FILE_SIZE = 1_000_000; // 1 MB — matches backend schema limit

export interface EnvFileUploadButtonProps {
  /**
   * Called with the parsed key/value pairs once a valid file has been read.
   */
  onParsed: (entries: ParsedEnvEntry[]) => void;
  disabled?: boolean;
  label?: string;
}

/**
 * A hidden-file-input upload button that reads a `.env`/text file with
 * FileReader and hands the parsed entries to the caller to merge into its
 * own env var state.
 */
export function EnvFileUploadButton({
  onParsed,
  disabled = false,
  label = 'Upload .env file',
}: EnvFileUploadButtonProps) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploadError(null);

    if (file.size > MAX_FILE_SIZE) {
      setUploadError(`File exceeds 1 MB limit (${(file.size / 1_000_000).toFixed(1)} MB)`);
      e.target.value = '';
      return;
    }

    const reader = new FileReader();
    reader.onload = () => {
      const entries = parseEnvFileContent(reader.result as string);
      if (entries.length === 0) {
        setUploadError('No key-value pairs found in the uploaded file');
        return;
      }
      onParsed(entries);
    };
    reader.onerror = () => {
      setUploadError('Failed to read file');
    };
    reader.readAsText(file, 'utf-8');
    e.target.value = '';
  };

  return (
    <>
      <input
        type="file"
        ref={fileInputRef}
        accept=".env,.txt,text/plain"
        onChange={handleFileUpload}
        style={{ display: 'none' }}
      />
      <Button
        size="small"
        variant="outlined"
        startIcon={<UploadIcon size={14} />}
        onClick={() => fileInputRef.current?.click()}
        disabled={disabled}
      >
        {label}
      </Button>
      {uploadError && (
        <Alert severity="error" sx={{ mt: 1, py: 0.5 }} onClose={() => setUploadError(null)}>
          {uploadError}
        </Alert>
      )}
    </>
  );
}

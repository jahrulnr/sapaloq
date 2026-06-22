// Attachment ingestion: files picked via the + button, pasted, dropped in the
// browser (File objects), or dropped natively on Wails (host paths). Each is
// inserted into the compose box as an inline pill.
import { ReadDroppedFile } from '../../wailsjs/go/main/App';
import type { AttachmentData } from '../ui/compose';
import { appendMessage } from './messages';
import { getCompose } from '../core/state';

function fileToDataURI(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('failed to read file'));
    reader.readAsDataURL(file);
  });
}

function fileToText(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('failed to read file'));
    reader.readAsText(file);
  });
}

// Insert files picked via the + button, pasted, or dropped in-browser as inline
// pills at the caret. Images keep a dataURI (for inline vision); other files are
// read as text. None of these carry a host path (browser sandbox), so the pill
// holds the dataURI/text payload.
export async function addFiles(files: FileList | File[]) {
  const compose = getCompose();
  const incoming = Array.from(files).filter(Boolean);
  if (!incoming.length || !compose) return;
  const wrap = document.getElementById('compose-wrap');
  wrap?.classList.add('is-loading');
  try {
    for (const file of incoming) {
      const att: AttachmentData = file.type.startsWith('image/')
        ? { name: file.name || 'pasted-image', type: file.type, size: file.size, dataURI: await fileToDataURI(file) }
        : { name: file.name || 'pasted-file', type: file.type || 'text/plain', size: file.size, text: await fileToText(file) };
      compose.insertAttachment(att);
    }
  } finally {
    wrap?.classList.remove('is-loading');
    compose.focus();
  }
}

export async function addClipboardItems(clipboard: DataTransfer | null) {
  if (!clipboard) return false;
  const files = collectTransferFiles(clipboard);
  if (!files.length) return false;
  await addFiles(files);
  return true;
}

// Ingest native (Wails OnFileDrop) file paths. WebKitGTK cannot hand File
// objects to the webview for out-of-browser drops, so the drag is handled in
// GTK and we get paths back. Each path is read Go-side via ReadDroppedFile and
// inserted as an inline pill at the caret, carrying the real host `path`. That
// path flows to the model when the message is serialized (a model-visible
// `[Local file: <path>]` block, base64 dropped for path-backed binaries), which
// stops a delegated sub-agent from losing the file.
export async function addDroppedPaths(paths: string[]) {
  const compose = getCompose();
  const incoming = paths.map((p) => p.trim()).filter(Boolean);
  if (!incoming.length || !compose) return;
  const wrap = document.getElementById('compose-wrap');
  wrap?.classList.add('is-loading');
  try {
    for (const path of incoming) {
      try {
        const file = await ReadDroppedFile(path);
        if (!file) continue;
        compose.insertAttachment({
          name: file.name,
          path: file.path || undefined,
          type: file.mime || (file.is_image ? 'image/*' : 'text/plain'),
          size: file.size,
          dataURI: file.data_uri || undefined,
          text: file.text || undefined,
        });
      } catch (err) {
        appendMessage('message--error', `gagal membaca ${path.split('/').pop()}: ${String(err)}`);
      }
    }
  } finally {
    wrap?.classList.remove('is-loading');
    compose.focus();
  }
}

function dataURIToFile(dataURI: string, fallbackName = 'dropped-image'): File | null {
  const match = /^data:([^;,]+)?(;base64)?,(.*)$/.exec(dataURI.trim());
  if (!match) return null;
  const mime = match[1] || 'application/octet-stream';
  const isBase64 = Boolean(match[2]);
  const payload = match[3];
  let bytes: Uint8Array;
  try {
    const bin = isBase64 ? atob(payload) : decodeURIComponent(payload);
    bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  } catch {
    return null;
  }
  const ext = (mime.split('/')[1] || 'bin').split(';')[0];
  return new File([bytes], `${fallbackName}.${ext}`, { type: mime });
}

// Collect File objects from a drop DataTransfer. Some sources (file managers on
// WebKitGTK, in-window image drags) populate `items` but leave `files` empty, so
// we must read both. getAsFile() only works while the drop event is live, hence
// this stays synchronous. As a last resort, rendered-image drags expose only a
// URL string — convert data:image/... URIs to a File.
export function collectTransferFiles(transfer: DataTransfer | null): File[] {
  if (!transfer) return [];
  const files: File[] = [];
  const seen = new Set<string>();
  const push = (file: File | null | undefined) => {
    if (!file) return;
    const key = `${file.name}|${file.size}|${file.type}`;
    if (seen.has(key)) return;
    seen.add(key);
    files.push(file);
  };
  for (const file of Array.from(transfer.files || [])) push(file);
  for (const item of Array.from(transfer.items || [])) {
    if (item.kind === 'file') push(item.getAsFile());
  }
  if (!files.length) {
    const uriList = transfer.getData('text/uri-list') || transfer.getData('text/plain') || '';
    for (const line of uriList.split(/\r?\n/)) {
      const uri = line.trim();
      if (uri.startsWith('data:image/')) push(dataURIToFile(uri));
    }
  }
  return files;
}

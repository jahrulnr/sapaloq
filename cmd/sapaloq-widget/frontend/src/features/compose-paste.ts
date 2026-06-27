// Unified compose paste: sync DataTransfer (Ctrl+V), Async Clipboard API, then
// Wails/GTK fallbacks. WebKitGTK in Wails often blocks navigator.clipboard and
// makes execCommand('paste') a no-op while still returning true.

import { ClipboardGetImage } from '../../wailsjs/go/main/App';
import { ClipboardGetText } from '../../wailsjs/runtime/runtime';
import type { ComposeBox } from '../ui/compose';
import { addFiles, collectTransferFiles, dataURIToFile } from './attachments';
import { collectClipboardImageFiles } from './clipboard';

function plainFromTransfer(transfer: DataTransfer): string {
  const direct = transfer.getData('text/plain')?.trim();
  if (direct) return direct;
  const uriList = transfer.getData('text/uri-list') || '';
  for (const line of uriList.split(/\r?\n/)) {
    const uri = line.trim();
    if (!uri || uri.startsWith('#')) continue;
    return uri;
  }
  return textFromHtml(transfer.getData('text/html'));
}

function textFromHtml(html: string): string {
  if (!html) return '';
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const text = doc.body.textContent?.trim();
  if (text) return text;
  return doc.querySelector('a[href]')?.getAttribute('href')?.trim() || '';
}

function imageFileFromHtml(html: string): File | null {
  if (!html) return null;
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const src = doc.querySelector('img[src]')?.getAttribute('src') || '';
  if (src.startsWith('data:image/')) return dataURIToFile(src);
  return null;
}

async function filesFromNavigatorClipboard(): Promise<File[]> {
  if (!navigator.clipboard?.read) return [];
  const files: File[] = [];
  try {
    for (const item of await navigator.clipboard.read()) {
      for (const type of item.types) {
        if (!type.startsWith('image/')) continue;
        try {
          const blob = await item.getType(type);
          const ext = (type.split('/')[1] || 'png').split(';')[0];
          files.push(new File([blob], `pasted-image.${ext}`, { type }));
        } catch {
          // Unsupported clipboard type on this webview build.
        }
      }
    }
  } catch {
    // Permission denied or unavailable outside a user gesture.
  }
  return files;
}

async function textFromNavigatorClipboard(): Promise<string> {
  if (!navigator.clipboard?.readText) return '';
  try {
    return (await navigator.clipboard.readText()).trim();
  } catch {
    return '';
  }
}

async function textFromWailsClipboard(): Promise<string> {
  try {
    return (await ClipboardGetText()).trim();
  } catch {
    return '';
  }
}

async function imageFromWailsClipboard(): Promise<{ dataURI: string; mime: string; size: number } | null> {
  try {
    const img = await ClipboardGetImage();
    if (!img?.data_uri) return null;
    return { dataURI: img.data_uri, mime: img.mime || 'image/png', size: img.size || 0 };
  } catch {
    return null;
  }
}

export async function ingestComposePaste(compose: ComposeBox, sync?: DataTransfer | null): Promise<boolean> {
  if (sync) {
    let files = collectTransferFiles(sync);
    if (!files.length) files = await collectClipboardImageFiles(sync);
    if (files.length) {
      await addFiles(files);
      return true;
    }

    const html = sync.getData('text/html');
    const htmlImage = imageFileFromHtml(html);
    if (htmlImage) {
      await addFiles([htmlImage]);
      return true;
    }

    const plain = plainFromTransfer(sync);
    if (plain) {
      compose.insertText(plain);
      return true;
    }
  }

  const navFiles = await filesFromNavigatorClipboard();
  if (navFiles.length) {
    await addFiles(navFiles);
    return true;
  }

  const navText = await textFromNavigatorClipboard();
  if (navText) {
    compose.insertText(navText);
    return true;
  }

  const wailsText = await textFromWailsClipboard();
  if (wailsText) {
    compose.insertText(wailsText);
    return true;
  }

  const wailsImage = await imageFromWailsClipboard();
  if (wailsImage) {
    compose.insertAttachment({
      name: 'pasted-image.png',
      type: wailsImage.mime,
      size: wailsImage.size,
      dataURI: wailsImage.dataURI,
    });
    return true;
  }

  return false;
}

// Clipboard helpers shared by the compose box and document-level paste fallback.
// WebKitGTK often exposes screenshots as `kind:"string"` + `type:"image/png"`.

export function transferHasAttachable(transfer: DataTransfer | null): boolean {
  if (!transfer) return false;
  if (transfer.files?.length) return true;
  for (const item of Array.from(transfer.items || [])) {
    if (item.kind === 'file') return true;
    if (item.kind === 'string' && item.type.startsWith('image/')) return true;
  }
  return false;
}

export async function collectClipboardImageFiles(transfer: DataTransfer | null): Promise<File[]> {
  if (!transfer) return [];
  const files: File[] = [];
  for (const item of Array.from(transfer.items || [])) {
    if (item.kind !== 'string' || !item.type.startsWith('image/')) continue;
    const getType = (item as DataTransferItem & { getType?: (type: string) => Promise<Blob> }).getType;
    if (!getType) continue;
    try {
      const blob = await getType.call(item, item.type);
      const ext = (item.type.split('/')[1] || 'png').split(';')[0];
      files.push(new File([blob], `pasted-image.${ext}`, { type: item.type }));
    } catch {
      // Clipboard item expired or type unsupported — skip.
    }
  }
  return files;
}

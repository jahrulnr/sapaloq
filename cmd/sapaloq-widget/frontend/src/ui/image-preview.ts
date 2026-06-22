// Full-screen image preview overlay. Shared by markdown image clicks and
// message attachment thumbnails, so it lives in its own module to keep the
// markdown/messages modules free of a circular dependency.
export function showImagePreview(src: string, alt = 'image') {
  document.getElementById('image-preview')?.remove();
  const overlay = document.createElement('button');
  overlay.type = 'button';
  overlay.id = 'image-preview';
  overlay.className = 'image-preview';
  overlay.setAttribute('aria-label', 'Close image preview');
  const image = document.createElement('img');
  image.src = src;
  image.alt = alt;
  overlay.append(image);
  overlay.addEventListener('click', () => overlay.remove());
  document.body.append(overlay);
}

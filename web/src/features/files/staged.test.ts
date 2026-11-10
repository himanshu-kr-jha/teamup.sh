import { describe, expect, it } from 'vitest';
import { kindOf } from './StagedFiles';

describe('kindOf', () => {
  it('previews pictures, video and recordings by their type', () => {
    expect(kindOf({ filename: 'a.png', contentType: 'image/png' })).toBe('image');
    expect(kindOf({ filename: 'a.mp4', contentType: 'video/mp4' })).toBe('video');
    expect(kindOf({ filename: 'a.webm', contentType: 'audio/webm' })).toBe('audio');
  });

  it('reads a PDF, even one the browser could not name', () => {
    expect(kindOf({ filename: 'Report.PDF', contentType: 'application/octet-stream' })).toBe('pdf');
  });

  it('shows text, whether the type or only the name says so', () => {
    expect(kindOf({ filename: 'notes.txt', contentType: 'text/plain' })).toBe('text');
    expect(kindOf({ filename: 'data.json', contentType: 'application/json' })).toBe('text');
    expect(kindOf({ filename: 'deploy.yaml', contentType: '' })).toBe('text');
  });

  it('has nothing to show for a Word document or an archive', () => {
    expect(kindOf({ filename: 'plan.docx', contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' })).toBe('other');
    expect(kindOf({ filename: 'all.zip', contentType: 'application/zip' })).toBe('other');
  });
});

describe('kindOf, by the exact type', () => {
  it('knows structured text by its type alone', () => {
    expect(kindOf({ filename: 'feed', contentType: 'application/atom+xml' })).toBe('text');
    expect(kindOf({ filename: 'config', contentType: 'application/x-yaml' })).toBe('text');
  });
});

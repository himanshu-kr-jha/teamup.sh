import { useRef, useState, type ChangeEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Camera, Loader2, Trash2 } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { UserAvatar } from '@/shared/components/UserAvatar';
import { orgApi } from '@/shared/api/client';
import { apiErrorMessage } from '@/shared/api/errors';
import { useToast } from '@/shared/hooks/useToast';
import { squareImage } from '@/shared/squareImage';
import { useAuth } from '@/features/auth/AuthProvider';

/** Pictures larger than this are not worth reading, even to shrink. */
const MAX_SOURCE_BYTES = 20 * 1024 * 1024;

/** Your own picture: chosen, shrunk to a square here, and shown to your organization. */
export function ProfilePicture() {
  const { user, refreshUser } = useAuth();
  const toast = useToast();
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);

  if (!user) return null;

  const done = async (message: string) => {
    await refreshUser();
    // Everywhere a name is shown with a picture reads it from one of these.
    for (const key of ['users', 'channels', 'messages', 'availability']) {
      void queryClient.invalidateQueries({ queryKey: [key] });
    }
    toast.success(message);
  };

  const choose = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    if (!file.type.startsWith('image/')) {
      toast.error('Choose a picture: a photo or an image file.');
      return;
    }
    if (file.size > MAX_SOURCE_BYTES) {
      toast.error('That picture is over 20 MB. Choose a smaller one.');
      return;
    }
    setBusy(true);
    try {
      const image = await squareImage(file);
      await orgApi.setAvatar(user.id, image);
      await done('Your picture has been updated.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not use that picture.'));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await orgApi.removeAvatar(user.id);
      await done('Your picture has been removed.');
    } catch (err) {
      toast.error(apiErrorMessage(err, 'Could not remove your picture.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-4">
      <div className="relative">
        <UserAvatar
          name={user.name}
          src={user.avatarUrl}
          className="h-20 w-20"
          fallbackClassName="text-2xl"
        />
        {busy && (
          <span className="absolute inset-0 flex items-center justify-center rounded-full bg-background/70">
            <Loader2 className="h-6 w-6 animate-spin" aria-label="Saving" />
          </span>
        )}
      </div>
      <div className="space-y-2">
        <input
          ref={input}
          type="file"
          accept="image/*"
          className="hidden"
          onChange={choose}
          aria-label="Profile picture file"
        />
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => input.current?.click()} disabled={busy}>
            <Camera className="mr-2 h-4 w-4" />
            {user.avatarUrl ? 'Change picture' : 'Upload picture'}
          </Button>
          {user.avatarUrl && (
            <Button type="button" variant="ghost" size="sm" onClick={remove} disabled={busy}>
              <Trash2 className="mr-2 h-4 w-4" />
              Remove
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          Cut to a square from the middle. Location and camera details in the photo are not kept.
        </p>
      </div>
    </div>
  );
}

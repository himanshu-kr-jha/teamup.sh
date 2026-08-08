import { create } from 'zustand';

export type ToastVariant = 'default' | 'destructive' | 'success';

export interface ToastMessage {
  id: string;
  title?: string;
  description: string;
  variant: ToastVariant;
}

interface ToastState {
  toasts: ToastMessage[];
  push: (toast: Omit<ToastMessage, 'id'>) => string;
  dismiss: (id: string) => void;
}

// The Toaster was already mounted but had nothing driving it, so every toast
// call in the app went nowhere. This is that missing state.
export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  push: (toast) => {
    const id = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    set((state) => ({ toasts: [...state.toasts, { ...toast, id }] }));
    return id;
  },
  dismiss: (id) => set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) })),
}));

/**
 * Report something to the user. `error` is the one that matters most: a failed
 * request used to reach only the console, so a broken action was
 * indistinguishable from a working one.
 */
export function useToast() {
  const push = useToastStore((s) => s.push);
  const dismiss = useToastStore((s) => s.dismiss);

  return {
    toast: (description: string, title?: string) => push({ description, title, variant: 'default' }),
    success: (description: string, title?: string) => push({ description, title, variant: 'success' }),
    error: (description: string, title = 'Something went wrong') =>
      push({ description, title, variant: 'destructive' }),
    dismiss,
  };
}

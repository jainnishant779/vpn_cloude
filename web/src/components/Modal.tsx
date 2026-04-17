import { Dialog, DialogPanel, DialogTitle } from "@headlessui/react";
import { ReactNode } from "react";

interface ModalProps {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
}

export default function Modal({ open, title, onClose, children }: ModalProps) {
  return (
    <Dialog open={open} onClose={onClose} className="relative z-50">
      <div className="fixed inset-0 bg-ink/45" aria-hidden="true" />
      <div className="fixed inset-0 flex items-center justify-center p-4">
        <DialogPanel className="card-surface w-full max-w-lg rounded-xl2 p-6 shadow-card fade-in">
          <DialogTitle className="text-xl font-semibold text-ink">{title}</DialogTitle>
          <div className="mt-4">{children}</div>
        </DialogPanel>
      </div>
    </Dialog>
  );
}

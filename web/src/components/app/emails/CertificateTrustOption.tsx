// The opt-out from certificate authority checks for a mailbox's own server.
// The connect and reconnect forms show it only after a test failed on the
// server's certificate, so nobody turns verification off by accident.

import { AnimatePresence, motion } from "framer-motion";
import { ShieldAlertIcon } from "lucide-react";

import { Checkbox } from "@/components/ui/checkbox";

export default function CertificateTrustOption({
    show,
    checked,
    onChange,
}: {
    show: boolean;
    checked: boolean;
    onChange: (v: boolean) => void;
}) {
    return (
        <AnimatePresence initial={false}>
            {show && (
                <motion.div
                    key="cert-trust"
                    initial={{ height: 0, opacity: 0 }}
                    animate={{ height: "auto", opacity: 1 }}
                    exit={{ height: 0, opacity: 0 }}
                    transition={{ duration: 0.2, ease: [0.32, 0.72, 0, 1] }}
                    className="overflow-hidden"
                >
                    <div className="mx-4 my-3 rounded-md border border-amber-200 bg-amber-50/70 px-3 py-2.5">
                        <div className="flex items-start gap-2">
                            <ShieldAlertIcon className="w-3.5 h-3.5 text-amber-600 shrink-0 mt-0.5" />
                            <div className="min-w-0 space-y-2">
                                <p className="text-[11.5px] text-amber-900 leading-relaxed">
                                    Your server's certificate is not one a recognised certificate authority vouches for.
                                    That is normal for a server with a self-signed or company certificate.
                                </p>
                                <label className="flex items-start gap-2 cursor-pointer">
                                    <Checkbox
                                        tone="slate"
                                        className="mt-0.5"
                                        checked={checked}
                                        onChange={(e) => onChange(e.target.checked)}
                                    />
                                    <span className="text-[11.5px] text-slate-700 leading-relaxed">
                                        Trust this server's own certificate. Warmbly remembers the certificate it sees now
                                        and stops connecting if it ever changes, until you confirm the new one.
                                    </span>
                                </label>
                            </div>
                        </div>
                    </div>
                </motion.div>
            )}
        </AnimatePresence>
    );
}

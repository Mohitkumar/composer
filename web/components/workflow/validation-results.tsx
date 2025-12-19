"use client";

import { Button } from "@/components/ui/button";
import type { ValidationResult } from "@/lib/workflow-validation";

interface ValidationResultsProps {
  validationResult: ValidationResult | null;
  showWarnings: boolean;
  onToggleWarnings: () => void;
}

export function ValidationResults({
  validationResult,
  showWarnings,
  onToggleWarnings,
}: ValidationResultsProps) {
  if (!validationResult) return null;

  return (
    <div className="space-y-1.5">
      {validationResult.errors.length > 0 && (
        <div className="p-2 rounded-md bg-destructive/10 border border-destructive/20">
          <p className="text-xs font-semibold text-destructive mb-1">
            Validation Errors ({validationResult.errors.length}):
          </p>
          <ul className="list-disc list-inside space-y-0.5 text-xs text-destructive">
            {validationResult.errors.map((error, idx) => (
              <li key={idx}>{error}</li>
            ))}
          </ul>
        </div>
      )}
      {validationResult.warnings.length > 0 && showWarnings && (
        <div className="p-2 rounded-md bg-yellow-500/10 border border-yellow-500/20">
          <p className="text-xs font-semibold text-yellow-700 dark:text-yellow-400 mb-1">
            Warnings ({validationResult.warnings.length}):
          </p>
          <ul className="list-disc list-inside space-y-0.5 text-xs text-yellow-700 dark:text-yellow-400">
            {validationResult.warnings.map((warning, idx) => (
              <li key={idx}>{warning}</li>
            ))}
          </ul>
        </div>
      )}
      {validationResult.isValid && validationResult.errors.length === 0 && (
        <div className="p-1.5 rounded-md bg-emerald-500/10 border border-emerald-500/20">
          <p className="text-xs font-semibold text-emerald-700 dark:text-emerald-400">
            ✓ Valid
          </p>
        </div>
      )}
    </div>
  );
}

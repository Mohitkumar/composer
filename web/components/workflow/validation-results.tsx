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
    <div className="space-y-2">
      {validationResult.errors.length > 0 && (
        <div className="p-3 rounded-md bg-destructive/10 border border-destructive/20">
          <p className="text-sm font-semibold text-destructive mb-2">
            Validation Errors ({validationResult.errors.length}):
          </p>
          <ul className="list-disc list-inside space-y-1 text-sm text-destructive">
            {validationResult.errors.map((error, idx) => (
              <li key={idx}>{error}</li>
            ))}
          </ul>
        </div>
      )}
      {validationResult.warnings.length > 0 && showWarnings && (
        <div className="p-3 rounded-md bg-yellow-500/10 border border-yellow-500/20">
          <p className="text-sm font-semibold text-yellow-700 dark:text-yellow-400 mb-2">
            Warnings ({validationResult.warnings.length}):
          </p>
          <ul className="list-disc list-inside space-y-1 text-sm text-yellow-700 dark:text-yellow-400">
            {validationResult.warnings.map((warning, idx) => (
              <li key={idx}>{warning}</li>
            ))}
          </ul>
        </div>
      )}
      {validationResult.isValid && validationResult.errors.length === 0 && (
        <div className="p-3 rounded-md bg-emerald-500/10 border border-emerald-500/20">
          <p className="text-sm font-semibold text-emerald-700 dark:text-emerald-400">
            ✓ Valid
          </p>
        </div>
      )}
    </div>
  );
}

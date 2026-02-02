'use client';

import { ReactNode, useState } from 'react';
import { Check, ChevronLeft, ChevronRight } from 'lucide-react';
import { Button } from '../common/Button';

interface Step {
  id: string;
  title: string;
  description?: string;
  content: ReactNode;
  validate?: () => boolean | Promise<boolean>;
}

interface MultiStepFormProps {
  steps: Step[];
  onComplete: () => void | Promise<void>;
  onCancel?: () => void;
  submitLabel?: string;
  loading?: boolean;
}

export function MultiStepForm({
  steps,
  onComplete,
  onCancel,
  submitLabel = 'Create',
  loading = false,
}: MultiStepFormProps) {
  const [currentStepIndex, setCurrentStepIndex] = useState(0);
  const [completedSteps, setCompletedSteps] = useState<Set<string>>(new Set());

  const currentStep = steps[currentStepIndex];
  const isFirstStep = currentStepIndex === 0;
  const isLastStep = currentStepIndex === steps.length - 1;

  const goToNextStep = async () => {
    if (currentStep.validate) {
      const isValid = await currentStep.validate();
      if (!isValid) return;
    }

    setCompletedSteps((prev) => new Set([...prev, currentStep.id]));

    if (isLastStep) {
      await onComplete();
    } else {
      setCurrentStepIndex((prev) => prev + 1);
    }
  };

  const goToPreviousStep = () => {
    if (!isFirstStep) {
      setCurrentStepIndex((prev) => prev - 1);
    }
  };

  const goToStep = (index: number) => {
    // Only allow going to completed steps or the current step
    if (index < currentStepIndex || completedSteps.has(steps[index].id)) {
      setCurrentStepIndex(index);
    }
  };

  return (
    <div className="space-y-8">
      {/* Step Indicator */}
      <nav aria-label="Progress">
        <ol className="flex items-center">
          {steps.map((step, index) => {
            const isCompleted = completedSteps.has(step.id);
            const isCurrent = index === currentStepIndex;
            const isClickable = index < currentStepIndex || isCompleted;

            return (
              <li
                key={step.id}
                className={`relative ${index !== steps.length - 1 ? 'flex-1' : ''}`}
              >
                <div className="flex items-center">
                  <button
                    onClick={() => isClickable && goToStep(index)}
                    disabled={!isClickable}
                    className={`relative flex h-10 w-10 items-center justify-center rounded-full transition-colors ${
                      isCompleted
                        ? 'bg-primary-600 text-white'
                        : isCurrent
                        ? 'border-2 border-primary-600 bg-white dark:bg-dark-900 text-primary-600'
                        : 'border-2 border-gray-300 dark:border-dark-600 bg-white dark:bg-dark-900 text-gray-500 dark:text-dark-400'
                    } ${isClickable ? 'cursor-pointer hover:opacity-80' : 'cursor-default'}`}
                  >
                    {isCompleted ? (
                      <Check className="w-5 h-5" />
                    ) : (
                      <span className="text-sm font-semibold">{index + 1}</span>
                    )}
                  </button>

                  {/* Connector Line */}
                  {index !== steps.length - 1 && (
                    <div
                      className={`flex-1 h-0.5 mx-4 ${
                        isCompleted
                          ? 'bg-primary-600'
                          : 'bg-gray-200 dark:bg-dark-700'
                      }`}
                    />
                  )}
                </div>

                {/* Step Label */}
                <div className="absolute -bottom-8 left-0 w-max">
                  <p
                    className={`text-sm font-medium ${
                      isCurrent || isCompleted
                        ? 'text-primary-600 dark:text-primary-400'
                        : 'text-gray-500 dark:text-dark-400'
                    }`}
                  >
                    {step.title}
                  </p>
                </div>
              </li>
            );
          })}
        </ol>
      </nav>

      {/* Step Content */}
      <div className="mt-16">
        <div className="mb-6">
          <h2 className="text-xl font-semibold text-gray-900 dark:text-white">
            {currentStep.title}
          </h2>
          {currentStep.description && (
            <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
              {currentStep.description}
            </p>
          )}
        </div>
        <div>{currentStep.content}</div>
      </div>

      {/* Navigation */}
      <div className="flex items-center justify-between pt-6 border-t border-gray-200 dark:border-dark-700">
        <div>
          {onCancel && (
            <Button variant="ghost" onClick={onCancel} disabled={loading}>
              Cancel
            </Button>
          )}
        </div>
        <div className="flex items-center gap-3">
          {!isFirstStep && (
            <Button
              variant="secondary"
              onClick={goToPreviousStep}
              disabled={loading}
              icon={<ChevronLeft className="w-4 h-4" />}
            >
              Previous
            </Button>
          )}
          <Button
            onClick={goToNextStep}
            loading={loading}
            icon={!isLastStep ? <ChevronRight className="w-4 h-4" /> : undefined}
          >
            {isLastStep ? submitLabel : 'Next'}
          </Button>
        </div>
      </div>
    </div>
  );
}

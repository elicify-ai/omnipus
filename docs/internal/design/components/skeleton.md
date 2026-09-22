# Skeleton

`Skeleton` reserves caller-supplied geometry immediately. Its visible shimmer waits 400ms and, once shown, remains for at least 300ms through `useLoadingVisibility`. `pending` is caller-controlled. The block is always decorative and hidden from assistive technology; callers cannot override its visibility or accessibility markers. Pulse and opacity transitions stop under reduced motion. Callers choose geometry that matches the content being loaded.

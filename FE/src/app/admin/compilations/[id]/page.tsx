"use client";

import { useParams } from "next/navigation";
import CompilationBuilder from "@/components/compilations/CompilationBuilder";

export default function CompilationPage() {
  const { id } = useParams<{ id: string }>();
  return <CompilationBuilder clipID={id} />;
}

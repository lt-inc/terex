"use client";
import { useParams } from "next/navigation";
import FlowEditor from "@/components/twitch/FlowEditor";
export default function FlowPage() {
  const { id } = useParams<{ id: string }>();
  return <FlowEditor flowID={id} />;
}

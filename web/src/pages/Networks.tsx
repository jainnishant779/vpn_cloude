import { FormEvent, useEffect, useState } from "react";
import LoadingSpinner from "../components/LoadingSpinner";
import Modal from "../components/Modal";
import NetworkCard from "../components/NetworkCard";
import { useNetworkStore } from "../store/networkStore";

export default function Networks() {
  const { networks, loading, error, fetchNetworks, createNetwork } = useNetworkStore();
  const [openModal, setOpenModal] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  useEffect(() => {
    void fetchNetworks();
  }, [fetchNetworks]);

  const handleCreateNetwork = async (event: FormEvent) => {
    event.preventDefault();
    if (!name.trim()) return;

    await createNetwork({ name: name.trim(), description: description.trim() });
    setName("");
    setDescription("");
    setOpenModal(false);
  };

  return (
    <div className="space-y-5 fade-in">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold text-ink">Networks</h2>
        <button
          type="button"
          onClick={() => setOpenModal(true)}
          className="rounded-lg bg-accent px-4 py-2 text-sm font-semibold text-white"
        >
          Create network
        </button>
      </div>

      {loading ? <LoadingSpinner label="Loading networks" /> : null}
      {error ? <p className="text-sm text-ember">{error}</p> : null}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {networks.map((network) => (
          <NetworkCard key={network.id} network={network} />
        ))}
      </div>

      <Modal open={openModal} title="Create network" onClose={() => setOpenModal(false)}>
        <form onSubmit={handleCreateNetwork} className="space-y-4">
          <label className="block">
            <span className="mb-1 block text-sm font-medium">Name</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              className="w-full rounded-lg border border-ink/20 px-3 py-2 outline-none focus:border-accent"
              placeholder="Development LAN"
            />
          </label>

          <label className="block">
            <span className="mb-1 block text-sm font-medium">Description</span>
            <textarea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={3}
              className="w-full rounded-lg border border-ink/20 px-3 py-2 outline-none focus:border-accent"
              placeholder="Devices used by frontend and backend teams"
            />
          </label>

          <div className="flex justify-end gap-3">
            <button
              type="button"
              onClick={() => setOpenModal(false)}
              className="rounded-lg border border-ink/20 px-4 py-2 text-sm"
            >
              Cancel
            </button>
            <button type="submit" className="rounded-lg bg-ink px-4 py-2 text-sm font-semibold text-white">
              Create
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

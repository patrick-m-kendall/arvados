# Copyright (C) The Arvados Authors. All rights reserved.
#
# SPDX-License-Identifier: AGPL-3.0

require 'can_be_an_owner'
require 'trashable'

class Group < ArvadosModel
  include HasUuid
  include KindAndEtag
  include CommonApiTemplate
  include CanBeAnOwner
  include Trashable

  # Posgresql JSONB columns should NOT be declared as serialized, Rails 5
  # already know how to properly treat them.
  attribute :properties, :jsonbHash, default: {}

  validate :ensure_filesystem_compatible_name
  validate :check_group_class
  validate :check_filter_group_filters
  before_create :assign_name
  after_create :after_ownership_change
  after_create :update_trash

  before_update :before_ownership_change
  after_update :after_ownership_change

  after_create :add_role_manage_link

  after_update :update_trash
  before_destroy :clear_permissions_and_trash

  api_accessible :user, extend: :common do |t|
    t.add :name
    t.add :group_class
    t.add :description
    t.add :writable_by
    t.add :delete_at
    t.add :trash_at
    t.add :is_trashed
    t.add :properties
  end

  def ensure_filesystem_compatible_name
    # project and filter groups need filesystem-compatible names, but others
    # don't.
    super if group_class == 'project' || group_class == 'filter'
  end

  def check_group_class
    if group_class != 'project' && group_class != 'role' && group_class != 'filter'
      errors.add :group_class, "value must be one of 'project', 'role' or 'filter', was '#{group_class}'"
    end
    if group_class_changed? && !group_class_was.nil?
      errors.add :group_class, "cannot be modified after record is created"
    end
  end

  def check_filter_group_filters
    if group_class == 'filter'
      if !self.properties.key?("filters")
        errors.add :properties, "filters property missing, it must be an array of arrays, each with 3 elements"
        return
      end
      if !self.properties["filters"].is_a?(Array)
        errors.add :properties, "filters property must be an array of arrays, each with 3 elements"
        return
      end
      self.properties["filters"].each do |filter|
        if !filter.is_a?(Array)
          errors.add :properties, "filters property must be an array of arrays, each with 3 elements"
          return
        end
        if filter.length() != 3
          errors.add :properties, "filters property must be an array of arrays, each with 3 elements"
          return
        end
        if !filter[0].include?(".") and filter[0].downcase != "uuid"
          errors.add :properties, "filter attribute must be 'uuid' or contain a dot (e.g. groups.name)"
          return
        end
        if (filter[0].downcase != "uuid" and filter[1].downcase == "is_a")
          errors.add :properties, "when filter operator is 'is_a', attribute must be 'uuid'"
          return
        end
        if ! ["=","<","<=",">",">=","!=","like","ilike","in","not in","is_a","exists","contains"].include?(filter[1].downcase)
          errors.add :properties, "filter operator is not valid (must be =,<,<=,>,>=,!=,like,ilike,in,not in,is_a,exists,contains)"
          return
        end
      end
    end
  end

  def update_trash
    if saved_change_to_trash_at? or saved_change_to_owner_uuid?
      # The group was added or removed from the trash.
      #
      # Strategy:
      #   Compute project subtree, propagating trash_at to subprojects
      #   Remove groups that don't belong from trash
      #   Add/update groups that do belong in the trash

      temptable = "group_subtree_#{rand(2**64).to_s(10)}"
      ActiveRecord::Base.connection.exec_query %{
create temporary table #{temptable} on commit drop
as select * from project_subtree_with_trash_at($1, LEAST($2, $3)::timestamp)
},
                                               'Group.update_trash.select',
                                               [[nil, self.uuid],
                                                [nil, TrashedGroup.find_by_group_uuid(self.owner_uuid).andand.trash_at],
                                                [nil, self.trash_at]]

      ActiveRecord::Base.connection.exec_delete %{
delete from trashed_groups where group_uuid in (select target_uuid from #{temptable} where trash_at is NULL);
},
                                            "Group.update_trash.delete"

      ActiveRecord::Base.connection.exec_query %{
insert into trashed_groups (group_uuid, trash_at)
  select target_uuid as group_uuid, trash_at from #{temptable} where trash_at is not NULL
on conflict (group_uuid) do update set trash_at=EXCLUDED.trash_at;
},
                                            "Group.update_trash.insert"
    end
  end

  def before_ownership_change
    if owner_uuid_changed? and !self.owner_uuid_was.nil?
      MaterializedPermission.where(user_uuid: owner_uuid_was, target_uuid: uuid).delete_all
      update_permissions self.owner_uuid_was, self.uuid, REVOKE_PERM
    end
  end

  def after_ownership_change
    if saved_change_to_owner_uuid?
      update_permissions self.owner_uuid, self.uuid, CAN_MANAGE_PERM
    end
  end

  def clear_permissions_and_trash
    MaterializedPermission.where(target_uuid: uuid).delete_all
    ActiveRecord::Base.connection.exec_delete %{
delete from trashed_groups where group_uuid=$1
}, "Group.clear_permissions_and_trash", [[nil, self.uuid]]

  end

  def assign_name
    if self.new_record? and (self.name.nil? or self.name.empty?)
      self.name = self.uuid
    end
    true
  end

  def ensure_owner_uuid_is_permitted
    if group_class == "role"
      @requested_manager_uuid = nil
      if new_record?
        @requested_manager_uuid = owner_uuid
        self.owner_uuid = system_user_uuid
        return true
      end
      if self.owner_uuid != system_user_uuid
        raise "Owner uuid for role must be system user"
      end
      raise PermissionDeniedError unless current_user.can?(manage: uuid)
      true
    else
      super
    end
  end

  def add_role_manage_link
    if group_class == "role" && @requested_manager_uuid
      act_as_system_user do
       Link.create!(tail_uuid: @requested_manager_uuid,
                    head_uuid: self.uuid,
                    link_class: "permission",
                    name: "can_manage")
      end
    end
  end
end
